// Package db wires up the SQLite connection and runs migrations.
package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open runs any pending migrations and returns a long-lived SQLite connection
// configured with WAL journaling and the standard pragmas.
//
// Migrations run on a throwaway connection because the migrate driver's Close
// closes the underlying *sql.DB.
func Open(path string) (*sql.DB, error) {
	if err := withMigrationConn(path, func(c *sql.DB) error {
		return migrateUp(c)
	}); err != nil {
		return nil, err
	}

	return openConn(path)
}

func openConn(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; one connection avoids "database is locked" under load.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return conn, nil
}

// withMigrationConn opens a fresh connection, runs fn, and ensures cleanup.
// The migrate driver closes the connection it owns; this helper exists so
// Open can run pending migrations on a throwaway connection without
// affecting the long-lived one it returns to the caller.
func withMigrationConn(path string, fn func(*sql.DB) error) error {
	conn, err := openConn(path)
	if err != nil {
		return err
	}
	return fn(conn)
}

func migrateUp(conn *sql.DB) error {
	m, err := newMigrate(conn)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _, _ = m.Close() }()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

func newMigrate(conn *sql.DB) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	driver, err := migratesqlite.WithInstance(conn, &migratesqlite.Config{})
	if err != nil {
		return nil, fmt.Errorf("migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite", driver)
	if err != nil {
		return nil, fmt.Errorf("init migrate: %w", err)
	}
	return m, nil
}
