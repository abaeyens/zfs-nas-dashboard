package store

import (
	"os"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndGetSince(t *testing.T) {
	s := newTestStore(t)

	if err := s.Insert("sda", 36.0); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Insert("sdb", 38.5); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := s.GetSince(1 * time.Hour)
	if err != nil {
		t.Fatalf("GetSince: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestGetSince_ExcludesOldRows(t *testing.T) {
	s := newTestStore(t)

	// Insert a row with a timestamp 2 hours in the past directly.
	old := time.Now().Add(-2 * time.Hour).Unix()
	_, err := s.db.Exec(`INSERT INTO temps (ts, disk, celsius) VALUES (?, ?, ?)`, old, "sda", 35.0)
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}

	rows, err := s.GetSince(1 * time.Hour)
	if err != nil {
		t.Fatalf("GetSince: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0 (old row should be excluded)", len(rows))
	}
}

func TestPrune(t *testing.T) {
	s := newTestStore(t)

	old := time.Now().Add(-2 * time.Hour).Unix()
	_, err := s.db.Exec(`INSERT INTO temps (ts, disk, celsius) VALUES (?, ?, ?)`, old, "sda", 35.0)
	if err != nil {
		t.Fatalf("direct insert: %v", err)
	}
	if err := s.Insert("sda", 36.0); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Prune anything older than 1 hour.
	if err := s.Prune(time.Now().Add(-1 * time.Hour)); err != nil {
		t.Fatalf("Prune: %v", err)
	}

	rows, err := s.GetSince(24 * time.Hour)
	if err != nil {
		t.Fatalf("GetSince: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows after prune, want 1", len(rows))
	}
	if rows[0].Disk != "sda" || rows[0].Celsius != 36.0 {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestCorruptDB_Recreated(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/temps.db"

	// Write garbage to simulate a corrupt DB.
	if err := os.WriteFile(path, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open should recover from corrupt DB, got: %v", err)
	}
	defer s.Close()

	// Should be usable after recovery.
	if err := s.Insert("sda", 40.0); err != nil {
		t.Fatalf("Insert after recovery: %v", err)
	}
}

func TestGetSince_OrderedByTS(t *testing.T) {
	s := newTestStore(t)

	base := time.Now().Add(-30 * time.Minute).Unix()
	for i := int64(3); i >= 1; i-- {
		_, err := s.db.Exec(`INSERT INTO temps (ts, disk, celsius) VALUES (?, ?, ?)`,
			base+i*60, "sda", float64(30+i))
		if err != nil {
			t.Fatal(err)
		}
	}

	rows, err := s.GetSince(1 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].TS < rows[i-1].TS {
			t.Errorf("rows not ordered by ts: row %d ts=%d < row %d ts=%d",
				i, rows[i].TS, i-1, rows[i-1].TS)
		}
	}
}
