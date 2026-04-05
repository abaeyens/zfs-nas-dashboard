package collector

import (
	"testing"

	"github.com/abaeyens/zfs-nas-dashboard/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		PoolName:    "vault",
		PoolPath:    "/vault",
		TempWarnC:   45,
		TempCritC:   55,
		ReallocWarn: 1,
		ReallocCrit: 5,
		PendingWarn: 1,
		PendingCrit: 5,
		UncorrWarn:  1,
		UncorrCrit:  5,
	}
}

// fakeRunner returns a CommandRunner that maps "binary args..." to canned output.
func fakeRunner(responses map[string]string) CommandRunner {
	return func(name string, args ...string) ([]byte, error) {
		key := name
		for _, a := range args {
			key += " " + a
		}
		// Try full key, then just the binary name as fallback.
		if v, ok := responses[key]; ok {
			return []byte(v), nil
		}
		return []byte(""), nil
	}
}

// ---- Smart tests ----

const zpoolStatusOutput = `  pool: vault
 state: ONLINE
  scan: scrub repaired 0B in 04:11:48 with 0 errors on Wed Apr  1 06:11:50 2026
config:

        NAME                                              STATE     READ WRITE CKSUM
        vault                                             ONLINE       0     0     0
          raidz2-0                                        ONLINE       0     0     0
            ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1  ONLINE       0     0     0
            ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T  ONLINE       0     0     0

errors: No known data errors`

const smartAttrOutput = `{
  "ata_smart_attributes": {
    "table": [
      {"id": 5,   "raw": {"value": 0}},
      {"id": 9,   "raw": {"value": 13819}},
      {"id": 194, "raw": {"value": 30}},
      {"id": 197, "raw": {"value": 0}},
      {"id": 198, "raw": {"value": 0}}
    ]
  }
}`

const smartInfoOutput = `{
  "model_name": "WDC WD40EFAX",
  "serial_number": "WD-ABC123"
}`

const smartHealthOutput = `{
  "smart_status": {"passed": true}
}`

func TestSmart_StatusStrings(t *testing.T) {
	cfg := testConfig()
	cfg.TempWarnC = 45
	cfg.TempCritC = 55

	run := fakeRunner(map[string]string{
		"zpool status -v vault": zpoolStatusOutput,
		"smartctl -i -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": smartInfoOutput,
		"smartctl -A -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": smartAttrOutput,
		"smartctl -H -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": smartHealthOutput,
		"smartctl -i -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": smartInfoOutput,
		"smartctl -A -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": smartAttrOutput,
		"smartctl -H -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": smartHealthOutput,
	})

	disks, err := Smart(cfg, run)
	if err != nil {
		t.Fatalf("Smart: %v", err)
	}
	if len(disks) != 2 {
		t.Fatalf("got %d disks, want 2", len(disks))
	}

	d := disks[0]
	if d.Celsius != 30 {
		t.Errorf("Celsius: got %d, want 30", d.Celsius)
	}
	if d.CelsiusStatus != "green" {
		t.Errorf("CelsiusStatus: got %q, want green", d.CelsiusStatus)
	}
	if d.Health != "PASSED" {
		t.Errorf("Health: got %q, want PASSED", d.Health)
	}
	if d.ReallocSectors != 0 {
		t.Errorf("ReallocSectors: got %d, want 0", d.ReallocSectors)
	}
	if d.ReallocStatus != "green" {
		t.Errorf("ReallocStatus: got %q, want green", d.ReallocStatus)
	}
}

func TestSmart_TempThresholds(t *testing.T) {
	cases := []struct {
		temp   int
		warn   int
		crit   int
		want   string
	}{
		{30, 45, 55, "green"},
		{45, 45, 55, "amber"},
		{55, 45, 55, "red"},
		{60, 45, 55, "red"},
		{44, 45, 55, "green"},
	}
	for _, c := range cases {
		got := tempStatus(c.temp, c.warn, c.crit)
		if got != c.want {
			t.Errorf("tempStatus(%d, %d, %d) = %q, want %q", c.temp, c.warn, c.crit, got, c.want)
		}
	}
}

func TestSmart_ThresholdStatus(t *testing.T) {
	cases := []struct {
		val  int64
		warn int64
		crit int64
		want string
	}{
		{0, 1, 5, "green"},
		{1, 1, 5, "amber"},
		{4, 1, 5, "amber"},
		{5, 1, 5, "red"},
		{10, 1, 5, "red"},
	}
	for _, c := range cases {
		got := thresholdStatus(c.val, c.warn, c.crit)
		if got != c.want {
			t.Errorf("thresholdStatus(%d, %d, %d) = %q, want %q", c.val, c.warn, c.crit, got, c.want)
		}
	}
}

func TestSmart_TempFallbackAttr190(t *testing.T) {
	attrWith190 := `{
  "ata_smart_attributes": {
    "table": [
      {"id": 190, "raw": {"value": 42}},
      {"id": 5,   "raw": {"value": 0}},
      {"id": 9,   "raw": {"value": 1000}},
      {"id": 197, "raw": {"value": 0}},
      {"id": 198, "raw": {"value": 0}}
    ]
  }
}`
	cfg := testConfig()
	run := fakeRunner(map[string]string{
		"zpool status -v vault": zpoolStatusOutput,
		"smartctl -i -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": smartInfoOutput,
		"smartctl -A -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": attrWith190,
		"smartctl -H -j /dev/disk/by-id/ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1": smartHealthOutput,
		"smartctl -i -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": smartInfoOutput,
		"smartctl -A -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": attrWith190,
		"smartctl -H -j /dev/disk/by-id/ata-WDC_WD40EFZX-68AWUN0_WD-WX12DA0EAU0T": smartHealthOutput,
	})

	disks, err := Smart(cfg, run)
	if err != nil {
		t.Fatalf("Smart: %v", err)
	}
	if len(disks) == 0 {
		t.Fatal("expected at least one disk")
	}
	if disks[0].Celsius != 42 {
		t.Errorf("Celsius fallback: got %d, want 42", disks[0].Celsius)
	}
}
