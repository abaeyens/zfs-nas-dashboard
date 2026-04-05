package collector

import (
	"fmt"
	"os/user"
	"strconv"
	"strings"

	"github.com/arend/nas-dashboard/internal/config"
)

// DirTree represents one node in the directory size tree.
type DirTree struct {
	Name      string     `json:"name"`
	Path      string     `json:"path"`
	SizeBytes int64      `json:"size_bytes"`
	Children  []*DirTree `json:"children,omitempty"`
}

// UserUsage represents disk space owned by one UNIX user (or the "system" group).
type UserUsage struct {
	User      string `json:"user"`
	SizeBytes int64  `json:"size_bytes"`
	Status    string `json:"status"` // always "green" — reserved for future quota thresholds
}

// FilesResult is the payload returned by GET /api/files.
type FilesResult struct {
	Tree  *DirTree    `json:"tree"`
	Users []UserUsage `json:"users"`
}

// Files runs du and find against the pool path and returns the result.
func Files(cfg *config.Config, run CommandRunner) (*FilesResult, error) {
	tree, err := collectDirTree(cfg, run)
	if err != nil {
		return nil, fmt.Errorf("dir tree: %w", err)
	}

	users, err := collectUserUsage(cfg, run)
	if err != nil {
		return nil, fmt.Errorf("user usage: %w", err)
	}

	return &FilesResult{Tree: tree, Users: users}, nil
}

// collectDirTree runs `du -x --block-size=1 --max-depth N` and builds a tree.
func collectDirTree(cfg *config.Config, run CommandRunner) (*DirTree, error) {
	maxDepth := strconv.Itoa(cfg.ScanDepth)
	out, err := run("du", "-x", "--block-size=1", "--max-depth="+maxDepth, cfg.PoolPath)
	if err != nil {
		return nil, fmt.Errorf("du: %w", err)
	}
	return parseDuOutput(string(out), cfg.PoolPath), nil
}

// parseDuOutput turns `du` output into a DirTree. du prints deepest paths first,
// so we collect all entries then build the tree bottom-up.
func parseDuOutput(output, root string) *DirTree {
	type entry struct {
		path  string
		bytes int64
	}
	var entries []entry

	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		size, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		path := strings.Join(fields[1:], " ")
		entries = append(entries, entry{path: path, bytes: size})
	}

	// Build a map of path → node.
	nodes := map[string]*DirTree{}
	for _, e := range entries {
		parts := strings.Split(strings.TrimPrefix(e.path, root), "/")
		name := parts[len(parts)-1]
		if name == "" {
			name = root
		}
		nodes[e.path] = &DirTree{
			Name:      name,
			Path:      e.path,
			SizeBytes: e.bytes,
		}
	}

	// Wire children to parents.
	for _, e := range entries {
		if e.path == root {
			continue
		}
		// Parent is the longest matching prefix that exists in nodes.
		parent := parentPath(e.path, nodes)
		if parent != nil {
			parent.Children = append(parent.Children, nodes[e.path])
		}
	}

	if n, ok := nodes[root]; ok {
		return n
	}
	// Root not found — return a minimal node.
	return &DirTree{Name: root, Path: root}
}

// parentPath finds the nearest ancestor of path that exists in nodes.
func parentPath(path string, nodes map[string]*DirTree) *DirTree {
	idx := strings.LastIndex(path, "/")
	if idx <= 0 {
		return nil
	}
	parent := path[:idx]
	if n, ok := nodes[parent]; ok {
		return n
	}
	return parentPath(parent, nodes)
}

// collectUserUsage runs find and tallies bytes per owner.
// Owners with uid < 1000 are grouped into a single "system" entry.
func collectUserUsage(cfg *config.Config, run CommandRunner) ([]UserUsage, error) {
	maxDepth := strconv.Itoa(cfg.ScanDepth)
	out, err := run("find", cfg.PoolPath, "-xdev", "-maxdepth", maxDepth, "-printf", "%U %b\n")
	if err != nil {
		return nil, fmt.Errorf("find: %w", err)
	}
	return parseUserUsage(string(out)), nil
}

// parseUserUsage tallies 512-byte blocks per uid and resolves uid → username.
// uid < 1000 are grouped as "system".
func parseUserUsage(output string) []UserUsage {
	tally := map[string]int64{} // username → bytes
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		uid := fields[0]
		blocks, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			continue
		}
		bytes := blocks * 512

		uidInt, err := strconv.Atoi(uid)
		if err != nil || uidInt < 1000 {
			tally["system"] += bytes
			continue
		}

		// Resolve uid → username; fall back to uid string on error.
		username := uid
		if u, err := user.LookupId(uid); err == nil {
			username = u.Username
		}
		tally[username] += bytes
	}

	var users []UserUsage
	for name, bytes := range tally {
		users = append(users, UserUsage{User: name, SizeBytes: bytes, Status: "green"})
	}
	return users
}
