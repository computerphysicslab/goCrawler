package storelib

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db   *sql.DB
	path string
}

// newSQLiteStore opens the SQLite cache database and creates its schema.
func newSQLiteStore(path string) (Store, error) {
	if err := ensureDir(path); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One writer is all the crawler needs; it also avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	statements := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=FULL;",
		"PRAGMA busy_timeout=5000;",
		`CREATE TABLE IF NOT EXISTS cache (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", stmt, err)
		}
	}

	return &sqliteStore{db: db, path: path}, nil
}

// Name returns a human-readable backend identifier.
func (s *sqliteStore) Name() string { return "SQLite (" + s.path + ")" }

// Exists reports whether a key is present in the cache.
func (s *sqliteStore) Exists(key string) bool {
	operations.Add(1)
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM cache WHERE key = ?`, key).Scan(&one)
	return err == nil
}

// Get returns the cached value of a key.
func (s *sqliteStore) Get(key string) string {
	operations.Add(1)
	var value string
	if err := s.db.QueryRow(`SELECT value FROM cache WHERE key = ?`, key).Scan(&value); err != nil {
		return ""
	}
	return value
}

// Set inserts or updates a cached value.
func (s *sqliteStore) Set(key, value string) error {
	operations.Add(1)
	_, err := s.db.Exec(`INSERT INTO cache(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=CURRENT_TIMESTAMP`, key, value)
	return err
}

// Close closes the database handle.
func (s *sqliteStore) Close() error {
	return s.db.Close()
}
