package collector

import (
	"testing"
)

const duOutput = `3100731037536	/vault/pictures
42104576712	/vault/backup
6981232154688	/vault`

const findOutput = `1000 100
1000 200
0 50
65534 30
1001 400`

func TestParseDuOutput(t *testing.T) {
	tree := parseDuOutput(duOutput, "/vault")

	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	if tree.Path != "/vault" {
		t.Errorf("root path: got %q", tree.Path)
	}
	if tree.SizeBytes != 6981232154688 {
		t.Errorf("root SizeBytes: got %d", tree.SizeBytes)
	}
	if len(tree.Children) != 2 {
		t.Errorf("root children: got %d, want 2", len(tree.Children))
	}
}

func TestParseUserUsage_SystemGrouping(t *testing.T) {
	// uid 0 and uid 65534 both go to "system"
	// uid 1000 → human user
	users := parseUserUsage(findOutput)

	totals := map[string]int64{}
	for _, u := range users {
		totals[u.User] += u.SizeBytes
	}

	// Only uid 0 (50 blocks × 512 = 25600 bytes) goes to system.
	// uid 65534 is >= 1000 so it is treated as a human user per spec.
	if totals["system"] != 25600 {
		t.Errorf("system bytes: got %d, want 25600", totals["system"])
	}

	// uid 1000: (100+200) blocks × 512 = 153600
	// uid 1001:  400 blocks × 512 = 204800
	// uid 65534: 30 blocks × 512  =  15360
	// total human = 373760
	var human int64
	for name, bytes := range totals {
		if name != "system" {
			human += bytes
		}
	}
	if human != 373760 {
		t.Errorf("human total bytes: got %d, want 373760", human)
	}
}

func TestParseUserUsage_StatusAlwaysGreen(t *testing.T) {
	users := parseUserUsage(findOutput)
	for _, u := range users {
		if u.Status != "green" {
			t.Errorf("user %q status: got %q, want green", u.User, u.Status)
		}
	}
}
