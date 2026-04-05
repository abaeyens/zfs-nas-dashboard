// Package poller owns the long-running background goroutines that collect
// data on a schedule and keep an in-memory cache.  HTTP handlers read the
// caches via the LatestXxx accessors — they never call collectors directly.
package poller

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/abaeyens/zfs-nas-dashboard/internal/broker"
	"github.com/abaeyens/zfs-nas-dashboard/internal/collector"
	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
	"github.com/abaeyens/zfs-nas-dashboard/internal/store"
)

// Poller holds the shared caches and co-ordinates the background goroutines.
type Poller struct {
	cfg    *config.Config
	store  *store.Store
	broker *broker.Broker

	smartMu sync.RWMutex
	smart   []collector.DiskInfo

	filesMu sync.RWMutex
	files   *collector.FilesResult
}

// New creates a Poller wired to the given dependencies.
func New(cfg *config.Config, st *store.Store, br *broker.Broker) *Poller {
	return &Poller{cfg: cfg, store: st, broker: br}
}

// Start launches the SMART and Files goroutines and returns immediately.
// The goroutines run until ctx is cancelled.
func (p *Poller) Start(ctx context.Context) {
	go p.runSmart(ctx)
	go p.runFiles(ctx)
}

// LatestSMART returns a snapshot of the most-recent SMART collection.
func (p *Poller) LatestSMART() []collector.DiskInfo {
	p.smartMu.RLock()
	defer p.smartMu.RUnlock()
	if p.smart == nil {
		return nil
	}
	cp := make([]collector.DiskInfo, len(p.smart))
	copy(cp, p.smart)
	return cp
}

// LatestFiles returns a snapshot of the most-recent files collection.
func (p *Poller) LatestFiles() *collector.FilesResult {
	p.filesMu.RLock()
	defer p.filesMu.RUnlock()
	return p.files
}

// runSmart polls SMART data on a fixed interval.
func (p *Poller) runSmart(ctx context.Context) {
	p.collectSmart() // collect once immediately
	ticker := time.NewTicker(p.cfg.SmartPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.collectSmart()
		}
	}
}

// runFiles polls directory and user usage on a fixed interval.
func (p *Poller) runFiles(ctx context.Context) {
	p.collectFiles() // collect once immediately
	ticker := time.NewTicker(p.cfg.FilesRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.collectFiles()
		}
	}
}

func (p *Poller) collectSmart() {
	disks, err := collector.Smart(p.cfg, collector.DefaultRunner)
	if err != nil {
		log.Error().Err(err).Msg("smart collection failed")
		return
	}

	p.smartMu.Lock()
	p.smart = disks
	p.smartMu.Unlock()

	// Persist temperatures.
	for _, d := range disks {
		if err := p.store.Insert(d.ByID, float64(d.Celsius)); err != nil {
			log.Error().Err(err).Str("disk", d.ByID).Msg("store insert failed")
		}
	}

	// Prune old records.
	cutoff := time.Now().Add(-time.Duration(p.cfg.TempHistoryHours) * time.Hour)
	if err := p.store.Prune(cutoff); err != nil {
		log.Error().Err(err).Msg("store prune failed")
	}

	// Broadcast update event.
	type smartEvent struct {
		Type  string               `json:"type"`
		Disks []collector.DiskInfo `json:"disks"`
	}
	evt := smartEvent{Type: "smart", Disks: disks}
	if b, err := json.Marshal(evt); err == nil {
		p.broker.Broadcast(b)
	}
}

func (p *Poller) collectFiles() {
	result, err := collector.Files(p.cfg, collector.DefaultRunner)
	if err != nil {
		log.Error().Err(err).Msg("files collection failed")
		return
	}

	p.filesMu.Lock()
	p.files = result
	p.filesMu.Unlock()
}

