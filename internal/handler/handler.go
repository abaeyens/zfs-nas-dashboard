// Package handler exposes the HTTP API and serves the embedded static frontend.
package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/abaeyens/zfs-nas-dashboard/internal/broker"
	"github.com/abaeyens/zfs-nas-dashboard/internal/collector"
	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
	"github.com/abaeyens/zfs-nas-dashboard/internal/poller"
	"github.com/abaeyens/zfs-nas-dashboard/internal/store"
	"github.com/abaeyens/zfs-nas-dashboard/web"
)

// NewRouter builds and returns the application router.
func NewRouter(cfg *config.Config, p *poller.Poller, br *broker.Broker, st *store.Store) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/hardware", onlyGET(handleHardware(cfg, p, st)))
	mux.HandleFunc("/api/zfs", onlyGET(handleZFS(cfg)))
	mux.HandleFunc("/api/files", onlyGET(handleFiles(p)))
	mux.HandleFunc("/api/events", handleEvents(br, p, cfg, st))

	// Serve embedded static files; fall back to index.html for SPA-style routing.
	mux.Handle("/", http.FileServer(http.FS(web.FS)))

	return mux
}

// onlyGET wraps a handler to return 405 for non-GET requests.
func onlyGET(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Error().Err(err).Msg("json encode")
	}
}

// handleHardware serves the latest SMART data plus temperature history.
func handleHardware(cfg *config.Config, p *poller.Poller, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		disks := p.LatestSMART()
		history, err := st.GetSince(time.Duration(cfg.TempHistoryHours) * time.Hour)
		if err != nil {
			log.Error().Err(err).Msg("store GetSince")
			history = nil
		}
		type response struct {
			Disks            []collector.DiskInfo `json:"disks"`
			History          []store.TempRow      `json:"history"`
			PollIntervalS    int                  `json:"poll_interval_s"`
			TempHistoryHours int                  `json:"temp_history_hours"`
		}
		writeJSON(w, response{Disks: disks, History: history, PollIntervalS: int(cfg.SmartPollInterval.Seconds()), TempHistoryHours: cfg.TempHistoryHours})
	}
}

// handleZFS calls the ZFS collector on every request (data is cheap to fetch
// and changes infrequently).
func handleZFS(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := collector.ZFS(cfg, collector.DefaultRunner)
		if err != nil {
			log.Error().Err(err).Msg("zfs collect")
			http.Error(w, "zfs collection failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, result)
	}
}

// handleFiles serves the cached files result.
func handleFiles(p *poller.Poller) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := p.LatestFiles()
		if result == nil {
			http.Error(w, "data not yet available", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, result)
	}
}

// handleEvents implements Server-Sent Events.
// On connection it sends an immediate snapshot of the current SMART data, then
// forwards every subsequent Broadcast from the broker until the client
// disconnects.
func handleEvents(br *broker.Broker, p *poller.Poller, cfg *config.Config, st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Send an init snapshot immediately so the page has data before the
		// first poll interval fires.
		disks := p.LatestSMART()
		history, _ := st.GetSince(time.Duration(cfg.TempHistoryHours) * time.Hour)
		type initPayload struct {
			Type             string               `json:"type"`
			Disks            []collector.DiskInfo `json:"disks"`
			History          []store.TempRow      `json:"history"`
			PollIntervalS    int                  `json:"poll_interval_s"`
			TempHistoryHours int                  `json:"temp_history_hours"`
		}
		if b, err := json.Marshal(initPayload{Type: "init", Disks: disks, History: history, PollIntervalS: int(cfg.SmartPollInterval.Seconds()), TempHistoryHours: cfg.TempHistoryHours}); err == nil {
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}

		ch := br.Register()
		defer br.Unregister(ch)

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}
