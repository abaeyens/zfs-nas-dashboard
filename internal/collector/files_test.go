package collector

import (
	"os"
	"path/filepath"
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

// TestFilterWorldReadable verifies that directories without the "other read"
// permission bit (0o004) are pruned from the tree, along with their subtrees.
func TestFilterWorldReadable(t *testing.T) {
	// Create a temporary directory structure:
	//   root/          (0755 — world-readable)
	//   root/pub/      (0755 — world-readable)
	//   root/priv/     (0700 — NOT world-readable)
	//   root/priv/sub/ (0755 — world-readable, but under a pruned parent)
	root := t.TempDir() // 0700 by default on some systems; we'll chmod explicitly

	pub := filepath.Join(root, "pub")
	priv := filepath.Join(root, "priv")
	sub := filepath.Join(priv, "sub")

	for _, dir := range []string{pub, priv, sub} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Make root and pub world-readable.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// priv is not world-readable.
	if err := os.Chmod(priv, 0o700); err != nil {
		t.Fatal(err)
	}

	tree := &DirTree{
		Name: "root",
		Path: root,
		Children: []*DirTree{
			{Name: "pub", Path: pub},
			{
				Name: "priv",
				Path: priv,
				Children: []*DirTree{
					{Name: "sub", Path: sub},
				},
			},
		},
	}

	result := filterWorldReadable(tree)

	if result == nil {
		t.Fatal("root should be kept (world-readable)")
	}
	if len(result.Children) != 1 {
		t.Fatalf("expected 1 child (pub), got %d", len(result.Children))
	}
	if result.Children[0].Name != "pub" {
		t.Errorf("expected kept child to be 'pub', got %q", result.Children[0].Name)
	}
}

// TestFilterWorldReadable_PrunesRoot verifies that a non-world-readable root returns nil.
func TestFilterWorldReadable_PrunesRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	tree := &DirTree{Name: "root", Path: root}
	if filterWorldReadable(tree) != nil {
		t.Error("expected nil for non-world-readable root")
	}
}
