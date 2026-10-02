// Package db owns the PostgreSQL connection pool, tenant-scoped transactions and migrations.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arshadm25/whatsapp_crm/internal/db/dbq"
)

// DB wraps the pool. Code reaches tenant tables only through InTenant, which sets app.tenant_id
// so the row-level security policies apply; queries still filter on tenant_id themselves.
type DB struct {
	Pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &DB{Pool: pool}, nil
}

func (d *DB) Close() { d.Pool.Close() }

// Global runs fn in a transaction with no tenant set. Only tables without RLS (users, tenants,
// sessions, platform tables) and the SECURITY DEFINER lookup functions are visible.
func (d *DB) Global(ctx context.Context, fn func(q *dbq.Queries, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		return fn(dbq.New(tx), tx)
	})
}

// InTenant runs fn in a transaction scoped to one tenant.
func (d *DB) InTenant(ctx context.Context, tenantID uuid.UUID, fn func(q *dbq.Queries, tx pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return errors.New("db: InTenant called without a tenant")
	}
	return pgx.BeginFunc(ctx, d.Pool, func(tx pgx.Tx) error {
		if err := SetTenant(ctx, tx, tenantID); err != nil {
			return err
		}
		return fn(dbq.New(tx), tx)
	})
}

// SetTenant sets app.tenant_id for the rest of the transaction.
func SetTenant(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String())
	return err
}

// IsNotFound reports whether err is a no-rows result.
func IsNotFound(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// IsUniqueViolation reports whether err is a unique constraint violation, optionally on a named constraint.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}

// NewID returns a time-ordered UUIDv7, the primary key convention for every table.
func NewID() uuid.UUID { return uuid.Must(uuid.NewV7()) }
