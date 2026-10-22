package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/pressly/goose/v3"

	"stasher/internal/migrations"
)

// Open opens the SQLite database at path and applies required pragmas.
func Open(path string) (*sql.DB, error) {
	d, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// modernc sqlite: set pragmas via DSN-free execution; single writer conn avoids SQLITE_BUSY.
	d.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	} {
		if _, err := d.Exec(pragma); err != nil {
			d.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return d, nil
}

var migrationsFS = migrations.FS

// Migrate applies all embedded migrations.
func Migrate(d *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	// Silence the per-migration "OK ..." log lines (noise in every test
	// run). goose v3.29 prints them through its package logger regardless
	// of SetVerbose (verbose only gates annotation echo and already
	// defaults false), so swap the logger for a nop. Migration failures
	// are returned as errors, not logged, so nothing is lost.
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite"); err != nil {
		return err
	}
	return goose.Up(d, ".")
}
