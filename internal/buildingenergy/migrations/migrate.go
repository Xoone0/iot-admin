// Package migrations embeds incremental SQL migrations for the building-energy
// management schema (energy_domain).
//
// IMPORTANT: Apply is NEVER auto-executed by cmd/server startup so existing
// production / FNOS databases remain untouched until explicit backup and
// deployment signoff.
package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var FS embed.FS

// Apply executes all pending incremental energy_domain migrations in ascending
// filename order, each inside its own transaction, recording versions in
// energy_domain.schema_migrations.
func Apply(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	if pool == nil {
		return nil, fmt.Errorf("energy migrations: nil pool")
	}
	files, err := fs.Glob(FS, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("energy migrations: glob: %w", err)
	}
	slices.Sort(files)

	// Ensure schema and migration ledger exist before checking applied versions.
	if _, err := pool.Exec(ctx, `
CREATE SCHEMA IF NOT EXISTS energy_domain;
CREATE TABLE IF NOT EXISTS energy_domain.schema_migrations (
    version    text PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);`); err != nil {
		return nil, fmt.Errorf("energy migrations: init ledger: %w", err)
	}

	var appliedNow []string
	for _, name := range files {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM energy_domain.schema_migrations WHERE version = $1)`, name).Scan(&exists); err != nil {
			return appliedNow, fmt.Errorf("energy migrations: check %s: %w", name, err)
		}
		if exists {
			continue
		}
		body, err := fs.ReadFile(FS, name)
		if err != nil {
			return appliedNow, fmt.Errorf("energy migrations: read %s: %w", name, err)
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(body)); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO energy_domain.schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING`, name)
			return err
		})
		if err != nil {
			return appliedNow, fmt.Errorf("energy migrations: apply %s: %w", name, err)
		}
		appliedNow = append(appliedNow, name)
	}
	return appliedNow, nil
}
