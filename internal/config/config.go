package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration, parsed from environment variables.
type Config struct {
	// Required
	PoolPath string
	PoolName string

	// HTTP
	Port string

	// Scanning
	ScanDepth            int
	TempHistoryHours     int
	SmartPollInterval    time.Duration
	FilesRefreshInterval time.Duration

	// Temperature thresholds (°C)
	TempWarnC int
	TempCritC int

	// SMART error thresholds
	ReallocWarn int
	ReallocCrit int
	PendingWarn int
	PendingCrit int
	UncorrWarn  int
	UncorrCrit  int

	// Storage
	DataDir string
}

// Load reads environment variables and returns a validated Config.
// Returns an error if any required variable is missing or any value is invalid.
func Load() (*Config, error) {
	var errs []error

	poolPath := os.Getenv("POOL_PATH")
	if poolPath == "" {
		errs = append(errs, errors.New("POOL_PATH is required"))
	}

	poolName := os.Getenv("POOL_NAME")
	if poolName == "" {
		errs = append(errs, errors.New("POOL_NAME is required"))
	}

	if len(errs) > 0 {
		return nil, joinErrors(errs)
	}

	cfg := &Config{
		PoolPath: poolPath,
		PoolName: poolName,
	}

	var parseErrs []error

	cfg.Port = envString("PORT", "8080")
	cfg.DataDir = envString("DATA_DIR", "/data")
	cfg.ScanDepth, parseErrs = appendInt(parseErrs, "SCAN_DEPTH", 5)
	cfg.TempHistoryHours, parseErrs = appendInt(parseErrs, "TEMP_HISTORY_HOURS", 6)
	cfg.TempWarnC, parseErrs = appendInt(parseErrs, "TEMP_WARN_C", 45)
	cfg.TempCritC, parseErrs = appendInt(parseErrs, "TEMP_CRIT_C", 55)
	cfg.ReallocWarn, parseErrs = appendInt(parseErrs, "REALLOC_WARN", 1)
	cfg.ReallocCrit, parseErrs = appendInt(parseErrs, "REALLOC_CRIT", 5)
	cfg.PendingWarn, parseErrs = appendInt(parseErrs, "PENDING_WARN", 1)
	cfg.PendingCrit, parseErrs = appendInt(parseErrs, "PENDING_CRIT", 5)
	cfg.UncorrWarn, parseErrs = appendInt(parseErrs, "UNCORR_WARN", 1)
	cfg.UncorrCrit, parseErrs = appendInt(parseErrs, "UNCORR_CRIT", 5)

	var smartSecs, filesSecs int
	smartSecs, parseErrs = appendInt(parseErrs, "SMART_POLL_INTERVAL", 60)
	filesSecs, parseErrs = appendInt(parseErrs, "FILES_REFRESH_INTERVAL", 60)
	cfg.SmartPollInterval = time.Duration(smartSecs) * time.Second
	cfg.FilesRefreshInterval = time.Duration(filesSecs) * time.Second

	if len(parseErrs) > 0 {
		return nil, joinErrors(parseErrs)
	}

	return cfg, nil
}

// MustLoad calls Load and panics if it returns an error.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic("config: " + err.Error())
	}
	return cfg
}

// envString returns the value of the named env var, or def if unset/empty.
func envString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// appendInt parses key as an integer, appending to errs on failure.
// Returns the parsed value (or def) and the updated error slice.
func appendInt(errs []error, key string, def int) (int, []error) {
	v := os.Getenv(key)
	if v == "" {
		return def, errs
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, append(errs, fmt.Errorf("%s: invalid integer %q", key, v))
	}
	return n, errs
}

func joinErrors(errs []error) error {
	msg := ""
	for i, e := range errs {
		if i > 0 {
			msg += "; "
		}
		msg += e.Error()
	}
	return errors.New(msg)
}
