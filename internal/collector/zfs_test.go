package collector

import (
	"testing"
)

const zpoolStatusForZFS = `  pool: vault
 state: ONLINE
  scan: scrub repaired 0B in 04:11:48 with 0 errors on Wed Apr  1 06:11:50 2026
config:

        NAME                                          STATE     READ WRITE CKSUM
        vault                                         ONLINE       0     0     0
          raidz2-0                                    ONLINE       0     0     0
            ata-WDC_WD40EFAX-68JH4N0_WD-WX32D7088AF1  ONLINE       0     0     0

errors: No known data errors`

const zfsListOutput = `vault	6981232154688	8865888544704	229152	1.07	zstd-1
vault/backup	42104576712	8865888544704	42104576712	1.37	zstd-1
vault/pictures	6312203290008	8865888544704	3100731037536	1.03	lz4`

const zfsSnapshotOutput = `vault/pictures@2026-04-05	2021448	1775383731
vault/pictures@2026-04-04	35002968	1775302425`

const arcstatsOutput = `9 1 0x01 147 39984 3703524900 1068624307064
name                            type data
hits                            4    1520969
misses                          4    95623
size                            4    12345678`

func TestParseZpoolStatus(t *testing.T) {
	pool := parseZpoolStatus(zpoolStatusForZFS, "vault")

	if pool.Name != "vault" {
		t.Errorf("Name: got %q", pool.Name)
	}
	if pool.State != "ONLINE" {
		t.Errorf("State: got %q, want ONLINE", pool.State)
	}
	if pool.Scan.Type != "scrub" {
		t.Errorf("Scan.Type: got %q, want scrub", pool.Scan.Type)
	}
	if pool.Scan.State != "finished" {
		t.Errorf("Scan.State: got %q, want finished", pool.Scan.State)
	}

	// Should have raidz2-0 and the one disk.
	if len(pool.VDevs) == 0 {
		t.Error("expected at least one vdev")
	}
	found := false
	for _, v := range pool.VDevs {
		if v.Name == "raidz2-0" {
			found = true
			if v.State != "ONLINE" {
				t.Errorf("raidz2-0 state: got %q, want ONLINE", v.State)
			}
		}
	}
	if !found {
		t.Error("raidz2-0 not found in vdevs")
	}
}

func TestParseZFSList(t *testing.T) {
	datasets := parseZFSList(zfsListOutput)

	if len(datasets) != 3 {
		t.Fatalf("got %d datasets, want 3", len(datasets))
	}
	if datasets[0].Name != "vault" {
		t.Errorf("first dataset name: %q", datasets[0].Name)
	}
	if datasets[0].UsedBytes != 6981232154688 {
		t.Errorf("UsedBytes: got %d", datasets[0].UsedBytes)
	}
	if datasets[1].CompressRatio != 1.37 {
		t.Errorf("CompressRatio: got %v, want 1.37", datasets[1].CompressRatio)
	}
}

func TestParseSnapshots(t *testing.T) {
	snaps := parseSnapshots(zfsSnapshotOutput)

	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2", len(snaps))
	}
	if snaps[0].Dataset != "vault/pictures" {
		t.Errorf("Dataset: got %q", snaps[0].Dataset)
	}
	if snaps[0].UsedBytes != 2021448 {
		t.Errorf("UsedBytes: got %d", snaps[0].UsedBytes)
	}
}

func TestParseARC(t *testing.T) {
	arc, err := parseARC([]byte(arcstatsOutput))
	if err != nil {
		t.Fatalf("parseARC: %v", err)
	}

	// hits=1520969, misses=95623 → rate ≈ 0.941
	if arc.HitRate < 0.94 || arc.HitRate > 0.95 {
		t.Errorf("HitRate: got %v, want ~0.941", arc.HitRate)
	}
	if arc.SizeBytes != 12345678 {
		t.Errorf("SizeBytes: got %d, want 12345678", arc.SizeBytes)
	}
	if arc.Status != "green" {
		t.Errorf("Status: got %q, want green", arc.Status)
	}
}

func TestParseARC_LowHitRate(t *testing.T) {
	lowHits := `9 1 0x01 1 1 1 1
name type data
hits   4 10
misses 4 90
size   4 1000`
	arc, err := parseARC([]byte(lowHits))
	if err != nil {
		t.Fatal(err)
	}
	if arc.Status != "red" {
		t.Errorf("Status: got %q, want red (hit rate 0.10)", arc.Status)
	}
}
