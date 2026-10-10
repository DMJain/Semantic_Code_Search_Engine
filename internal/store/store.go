// Package store owns the Postgres connection pool and schema migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver for goose
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// NewPool returns a pgx pool that understands the pgvector types. Connections
// are opened lazily, and opening one fails if the vector extension is not
// installed yet (run `indexer migrate` first).
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.AfterConnect = pgxvec.RegisterTypes
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}

// Migrate applies every pending migration (goose Up).
func Migrate(ctx context.Context, databaseURL string) ([]*goose.MigrationResult, error) {
	provider, err := newProvider(databaseURL)
	if err != nil {
		return nil, err
	}
	defer provider.Close()

	results, err := provider.Up(ctx)
	if err != nil {
		return results, fmt.Errorf("apply migrations: %w", err)
	}
	return results, nil
}

// MigrateDown rolls back exactly one migration (goose Down).
func MigrateDown(ctx context.Context, databaseURL string) (*goose.MigrationResult, error) {
	provider, err := newProvider(databaseURL)
	if err != nil {
		return nil, err
	}
	defer provider.Close()

	result, err := provider.Down(ctx)
	if err != nil {
		return result, fmt.Errorf("roll back migration: %w", err)
	}
	return result, nil
}

func newProvider(databaseURL string) (*goose.Provider, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		db.Close()
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	return provider, nil
}
