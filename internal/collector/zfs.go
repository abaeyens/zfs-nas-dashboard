package collector

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
)

// ZFSResult holds all ZFS data returned to the HTTP layer.
type ZFSResult struct {
	Pool      Pool       `json:"pool"`
	Datasets  []Dataset  `json:"datasets"`
	Snapshots []Snapshot `json:"snapshots"`
	ARC       ARCStats   `json:"arc"`

	SnapshotCount      int   `json:"snapshot_count"`
	SnapshotTotalBytes int64 `json:"snapshot_total_bytes"`
}

// Pool represents the top-level pool health.
type Pool struct {
	Name  string  `json:"name"`
	State string  `json:"state"`
	Scan  ScanInfo `json:"scan"`
	VDevs []VDev  `json:"vdevs"`
}

// ScanInfo holds the latest scrub/resilver result.
type ScanInfo struct {
	Type    string `json:"type"`    // "scrub" / "resilver"
	State   string `json:"state"`   // "finished" / "in_progress" / "none"
	EndTime string `json:"end_time"` // free-form from zpool output
	Errors  int    `json:"errors"`
}

// VDev is one entry from the pool config table.
type VDev struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	ReadErrors  int64  `json:"read_errors"`
	WriteErrors int64  `json:"write_errors"`
	CksumErrors int64  `json:"cksum_errors"`
}

// Dataset is a single ZFS dataset.
type Dataset struct {
	Name         string  `json:"name"`
	UsedBytes    int64   `json:"used_bytes"`
	AvailBytes   int64   `json:"avail_bytes"`
	ReferBytes   int64   `json:"refer_bytes"`
	CompressRatio float64 `json:"compress_ratio"`
	Compression  string  `json:"compression"`
}

// Snapshot is a single ZFS snapshot.
type Snapshot struct {
	Name        string `json:"name"`
	Dataset     string `json:"dataset"`
	Creation    string `json:"creation"`
	UsedBytes   int64  `json:"used_bytes"`
}

// ARCStats holds ARC hit rate and size.
type ARCStats struct {
	HitRate      float64 `json:"hit_rate"`
	SizeBytes    int64   `json:"size_bytes"`
	TotalRAMBytes int64  `json:"total_ram_bytes"`
	Status       string  `json:"status"` // green / amber / red
}

// ZFS collects all ZFS data.
func ZFS(cfg *config.Config, run CommandRunner) (*ZFSResult, error) {
	pool, err := collectPool(cfg, run)
	if err != nil {
		return nil, err
	}

	datasets, err := collectDatasets(cfg, run)
	if err != nil {
		return nil, err
	}

	snapshots, err := collectSnapshots(cfg, run)
	if err != nil {
		return nil, err
	}

	arc, err := collectARC()
	if err != nil {
		// Non-fatal: return empty ARC stats.
		arc = ARCStats{}
	}

	var totalSnap int64
	for _, s := range snapshots {
		totalSnap += s.UsedBytes
	}

	return &ZFSResult{
		Pool:               pool,
		Datasets:           datasets,
		Snapshots:          snapshots,
		ARC:                arc,
		SnapshotCount:      len(snapshots),
		SnapshotTotalBytes: totalSnap,
	}, nil
}

// collectPool runs `zpool status -p` and parses the output.
func collectPool(cfg *config.Config, run CommandRunner) (Pool, error) {
	out, err := run("zpool", "status", "-p", cfg.PoolName)
	if err != nil {
		return Pool{}, fmt.Errorf("zpool status: %w", err)
	}
	return parseZpoolStatus(string(out), cfg.PoolName), nil
}

// parseZpoolStatus parses the text output of `zpool status -p`.
func parseZpoolStatus(output, poolName string) Pool {
	pool := Pool{Name: poolName}
	var inConfig bool

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "state:") {
			pool.State = strings.TrimSpace(strings.TrimPrefix(trimmed, "state:"))
			continue
		}

		if strings.HasPrefix(trimmed, "scan:") {
			pool.Scan = parseScanLine(strings.TrimPrefix(trimmed, "scan:"))
			continue
		}

		if strings.HasPrefix(trimmed, "config:") {
			inConfig = true
			continue
		}

		if !inConfig {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		name := fields[0]
		state := fields[1]

		// Skip the pool root line and header.
		if name == poolName || name == "NAME" {
			continue
		}

		readErr, _ := strconv.ParseInt(fields[2], 10, 64)
		writeErr, _ := strconv.ParseInt(fields[3], 10, 64)
		cksumErr, _ := strconv.ParseInt(fields[4], 10, 64)

		pool.VDevs = append(pool.VDevs, VDev{
			Name:        name,
			State:       state,
			ReadErrors:  readErr,
			WriteErrors: writeErr,
			CksumErrors: cksumErr,
		})
	}
	return pool
}

func parseScanLine(s string) ScanInfo {
	s = strings.TrimSpace(s)
	info := ScanInfo{State: "none"}
	if strings.HasPrefix(s, "scrub") {
		info.Type = "scrub"
	} else if strings.HasPrefix(s, "resilver") {
		info.Type = "resilver"
	}
	if strings.Contains(s, "in progress") {
		info.State = "in_progress"
	} else if strings.Contains(s, "repaired") || strings.Contains(s, "with 0 errors") {
		info.State = "finished"
		// Extract " on <date>" — everything after "on "
		if idx := strings.LastIndex(s, " on "); idx >= 0 {
			info.EndTime = strings.TrimSpace(s[idx+4:])
		}
	}
	return info
}

// collectDatasets runs `zfs list` and parses the output.
// `zfs list` returns human-readable sizes; use `-p` for raw bytes.
func collectDatasets(cfg *config.Config, run CommandRunner) ([]Dataset, error) {
	out, err := run("zfs", "list", "-Hp", "-o", "name,used,avail,refer,compressratio,compression", cfg.PoolName)
	if err != nil {
		return nil, fmt.Errorf("zfs list: %w", err)
	}
	return parseZFSList(string(out)), nil
}

func parseZFSList(output string) []Dataset {
	var datasets []Dataset
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		used, _ := strconv.ParseInt(fields[1], 10, 64)
		avail, _ := strconv.ParseInt(fields[2], 10, 64)
		refer, _ := strconv.ParseInt(fields[3], 10, 64)
		// compressratio with -p is a raw decimal like "1.37", no "x" suffix.
		ratio, _ := strconv.ParseFloat(fields[4], 64)

		datasets = append(datasets, Dataset{
			Name:          fields[0],
			UsedBytes:     used,
			AvailBytes:    avail,
			ReferBytes:    refer,
			CompressRatio: ratio,
			Compression:   fields[5],
		})
	}
	return datasets
}

// collectSnapshots runs `zfs list -t snapshot` and parses the output.
func collectSnapshots(cfg *config.Config, run CommandRunner) ([]Snapshot, error) {
	out, err := run("zfs", "list", "-Hp", "-t", "snapshot", "-o", "name,used,creation", "-S", "creation", cfg.PoolName)
	if err != nil {
		// No snapshots is not an error.
		return nil, nil
	}
	return parseSnapshots(string(out)), nil
}

func parseSnapshots(output string) []Snapshot {
	var snaps []Snapshot
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[0]
		used, _ := strconv.ParseInt(fields[1], 10, 64)
		// creation is unix timestamp with -Hp
		creation := fields[2]

		dataset := name
		if idx := strings.Index(name, "@"); idx >= 0 {
			dataset = name[:idx]
		}

		snaps = append(snaps, Snapshot{
			Name:      name,
			Dataset:   dataset,
			Creation:  creation,
			UsedBytes: used,
		})
	}
	return snaps
}

// collectARC reads /proc/spl/kstat/zfs/arcstats directly (no command needed).
func collectARC() (ARCStats, error) {
	data, err := os.ReadFile("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return ARCStats{}, fmt.Errorf("read arcstats: %w", err)
	}
	return parseARC(data)
}

func parseARC(data []byte) (ARCStats, error) {
	values := map[string]int64{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 {
			v, err := strconv.ParseInt(fields[2], 10, 64)
			if err == nil {
				values[fields[0]] = v
			}
		}
	}

	hits := values["hits"]
	misses := values["misses"]
	size := values["size"]
	var hitRate float64
	if total := hits + misses; total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	// Read total RAM from /proc/meminfo.
	var totalRAM int64
	if mem, err := os.ReadFile("/proc/meminfo"); err == nil {
		totalRAM = parseTotalRAM(mem)
	}

	status := "red"
	switch {
	case hitRate >= 0.80:
		status = "green"
	case hitRate >= 0.50:
		status = "amber"
	}

	return ARCStats{
		HitRate:       hitRate,
		SizeBytes:     size,
		TotalRAMBytes: totalRAM,
		Status:        status,
	}, nil
}

func parseTotalRAM(data []byte) int64 {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.ParseInt(fields[1], 10, 64)
				if err == nil {
					return kb * 1024
				}
			}
		}
	}
	return 0
}
