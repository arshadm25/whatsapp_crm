package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies our schema migrations, then River's queue tables, using the owner role.
// It runs as a Helm pre-upgrade Job, so api, ingest and worker never hold DDL rights.
func Migrate(ctx context.Context, url string, log *slog.Logger) error {
	src, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5"+trimScheme(url))
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}
	v, _, _ := m.Version()
	log.Info("schema migrations applied", "version", v)

	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	rm, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return err
	}
	res, err := rm.Migrate(ctx, rivermigrate.DirectionUp, nil)
	if err != nil {
		return fmt.Errorf("migrate: river: %w", err)
	}
	log.Info("river migrations applied", "count", len(res.Versions))
	return nil
}

// trimScheme turns postgres://... into ://... so the pgx5 driver prefix can be put in front.
func trimScheme(url string) string {
	for _, p := range []string{"postgresql", "postgres"} {
		if len(url) > len(p) && url[:len(p)+1] == p+":" {
			return url[len(p):]
		}
	}
	return url
}
