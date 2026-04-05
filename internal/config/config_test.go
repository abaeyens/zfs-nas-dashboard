package config

import (
	"testing"
	"time"
)

func TestLoad_MissingRequired(t *testing.T) {
	t.Setenv("POOL_PATH", "")
	t.Setenv("POOL_NAME", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when POOL_PATH and POOL_NAME are missing")
	}
}

func TestLoad_MissingPoolPath(t *testing.T) {
	t.Setenv("POOL_PATH", "")
	t.Setenv("POOL_NAME", "vault")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when POOL_PATH is missing")
	}
}

func TestLoad_MissingPoolName(t *testing.T) {
	t.Setenv("POOL_PATH", "/vault")
	t.Setenv("POOL_NAME", "")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error when POOL_NAME is missing")
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("POOL_PATH", "/vault")
	t.Setenv("POOL_NAME", "vault")
	// Clear all optional vars so defaults apply
	for _, k := range []string{
		"PORT", "SCAN_DEPTH", "TEMP_HISTORY_HOURS",
		"SMART_POLL_INTERVAL", "FILES_REFRESH_INTERVAL",
		"TEMP_WARN_C", "TEMP_CRIT_C",
		"REALLOC_WARN", "REALLOC_CRIT",
		"PENDING_WARN", "PENDING_CRIT",
		"UNCORR_WARN", "UNCORR_CRIT",
		"DATA_DIR",
	} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, "8080"},
		{"ScanDepth", cfg.ScanDepth, 5},
		{"TempHistoryHours", cfg.TempHistoryHours, 6},
		{"SmartPollInterval", cfg.SmartPollInterval, 60 * time.Second},
		{"FilesRefreshInterval", cfg.FilesRefreshInterval, 60 * time.Second},
		{"TempWarnC", cfg.TempWarnC, 45},
		{"TempCritC", cfg.TempCritC, 55},
		{"ReallocWarn", cfg.ReallocWarn, 1},
		{"ReallocCrit", cfg.ReallocCrit, 5},
		{"PendingWarn", cfg.PendingWarn, 1},
		{"PendingCrit", cfg.PendingCrit, 5},
		{"UncorrWarn", cfg.UncorrWarn, 1},
		{"UncorrCrit", cfg.UncorrCrit, 5},
		{"DataDir", cfg.DataDir, "/data"},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoad_CustomValues(t *testing.T) {
	t.Setenv("POOL_PATH", "/tank")
	t.Setenv("POOL_NAME", "tank")
	t.Setenv("PORT", "9090")
	t.Setenv("TEMP_WARN_C", "40")
	t.Setenv("TEMP_CRIT_C", "50")
	t.Setenv("SMART_POLL_INTERVAL", "30")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "9090")
	}
	if cfg.TempWarnC != 40 {
		t.Errorf("TempWarnC: got %d, want 40", cfg.TempWarnC)
	}
	if cfg.TempCritC != 50 {
		t.Errorf("TempCritC: got %d, want 50", cfg.TempCritC)
	}
	if cfg.SmartPollInterval != 30*time.Second {
		t.Errorf("SmartPollInterval: got %v, want 30s", cfg.SmartPollInterval)
	}
}

func TestLoad_InvalidInt(t *testing.T) {
	t.Setenv("POOL_PATH", "/vault")
	t.Setenv("POOL_NAME", "vault")
	t.Setenv("SCAN_DEPTH", "notanumber")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid integer SCAN_DEPTH")
	}
}
