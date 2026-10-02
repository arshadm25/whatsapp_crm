// Package testdb gives integration tests a freshly migrated database, connected as the
// application role so row-level security is really enforced.
//
// Tests using it are skipped unless ECOGO_TEST_ADMIN_DATABASE_URL points at a Postgres 16
// superuser connection, for example postgres://postgres:postgres@localhost:5432/postgres.
package testdb

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/arshadm25/whatsapp_crm/internal/db"
)

const (
	ownerRole = "ecogo_test_owner"
	appRole   = "ecogo_app"
	password  = "ecogo_test"
)

// New creates a database for this test, migrates it as the owner role, and returns a pool
// connected as ecogo_app. The database is dropped when the test ends.
func New(t *testing.T) *db.DB {
	t.Helper()
	admin := os.Getenv("ECOGO_TEST_ADMIN_DATABASE_URL")
	if admin == "" {
		t.Skip("ECOGO_TEST_ADMIN_DATABASE_URL not set; skipping database test")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s' BYPASSRLS", ownerRole, password),
		fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", appRole, password),
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	name := "ecogo_test_" + strings.ToLower(strings.NewReplacer("/", "_", "-", "_").Replace(t.Name()))
	if len(name) > 60 {
		name = name[:60]
	}
	_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	if _, err := conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s", pgx.Identifier{name}.Sanitize(), ownerRole)); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	u.User = url.UserPassword(ownerRole, password)
	if err := db.Migrate(ctx, u.String(), slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	u.User = url.UserPassword(appRole, password)
	d, err := db.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open app pool: %v", err)
	}
	t.Cleanup(d.Close)
	return d
}
