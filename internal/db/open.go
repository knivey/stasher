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
	// modernc.org/sqlite executes _pragma DSN values verbatim per connection,
	// so every pooled connection starts with WAL + FK enforcement + a busy
	// timeout (function-call pragma form avoids "=" escaping in the query
	// string). The post-open loop below stays as a belt-and-suspenders
	// re-apply (idempotent) and as the error-reporting path.
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=busy_timeout(5000)"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// single writer conn avoids SQLITE_BUSY
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
