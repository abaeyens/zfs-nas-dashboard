package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	_ "modernc.org/sqlite"
)

const schema = `CREATE TABLE IF NOT EXISTS temps (
	ts      INTEGER NOT NULL,
	disk    TEXT    NOT NULL,
	celsius REAL    NOT NULL
);
CREATE INDEX IF NOT EXISTS temps_ts ON temps(ts);`

// TempRow is a single temperature reading from the database.
type TempRow struct {
	TS      int64   `json:"ts"`
	Disk    string  `json:"disk"`
	Celsius float64 `json:"celsius"`
}

// Store wraps a SQLite database for temperature persistence.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the SQLite database at dataDir/temps.db.
// If the file is corrupt it is deleted and recreated from scratch.
// The caller must call Close when done.
func Open(dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "temps.db")
	s, err := open(path)
	if err != nil {
		log.Warn().Err(err).Str("path", path).Msg("store: DB open failed, recreating")
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("store: remove corrupt DB: %w", removeErr)
		}
		s, err = open(path)
		if err != nil {
			return nil, fmt.Errorf("store: open after recreate: %w", err)
		}
	}

	// Prune at startup then daily.
	s.Prune(time.Now().Add(-7 * 24 * time.Hour))
	go s.dailyPrune()

	return s, nil
}

func open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Verify the DB is readable/writable (catches corrupt files that Open accepts).
	if _, err := db.Exec("SELECT 1 FROM temps LIMIT 1"); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, path: path}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Insert adds a temperature reading with the current Unix timestamp.
func (s *Store) Insert(disk string, celsius float64) error {
	_, err := s.db.Exec(
		`INSERT INTO temps (ts, disk, celsius) VALUES (?, ?, ?)`,
		time.Now().Unix(), disk, celsius,
	)
	return err
}

// GetSince returns all rows with ts > now-d, ordered by ts ascending.
func (s *Store) GetSince(d time.Duration) ([]TempRow, error) {
	cutoff := time.Now().Add(-d).Unix()
	rows, err := s.db.Query(
		`SELECT ts, disk, celsius FROM temps WHERE ts > ? ORDER BY ts ASC`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TempRow
	for rows.Next() {
		var r TempRow
		if err := rows.Scan(&r.TS, &r.Disk, &r.Celsius); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Prune deletes all rows older than cutoff.
func (s *Store) Prune(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM temps WHERE ts < ?`, cutoff.Unix())
	return err
}

func (s *Store) dailyPrune() {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-7 * 24 * time.Hour)
		if err := s.Prune(cutoff); err != nil {
			log.Warn().Err(err).Msg("store: daily prune failed")
		}
	}
}
