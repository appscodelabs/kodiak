// Package dbtest provides a Postgres-backed test harness for the web-api Go
// port. Tests calling New are skipped unless TEST_DATABASE_URL is set,
// mirroring pytest-django's requirement of a configured database.
//
// Run a throwaway server with:
//
//	docker run --rm -d --name kodiak-test-pg -p 5439:5432 \
//	    -e POSTGRES_PASSWORD=postgres postgres:16
//	export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5439/postgres'
//
// Run DB-backed tests with `go test -p 1 ./...` so packages sharing the
// database do not reset the schema concurrently.
package dbtest

import (
	"context"
	_ "embed"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// New returns a pool connected to TEST_DATABASE_URL with a freshly reset
// schema, giving each test the clean database state pytest-django provides
// per test. Skips the test when TEST_DATABASE_URL is unset.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-backed test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("reset test database schema: %v", err)
	}
	return pool
}
