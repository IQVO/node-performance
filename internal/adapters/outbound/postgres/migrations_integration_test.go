//go:build integration

package postgres_test

import (
	"context"
	"slices"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
)

// allTables is every table the migrations create, golang-migrate's own
// bookkeeping table excluded.
var allTables = []string{
	"idempotency_keys", "inbound_receipts", "inbound_shipments", "outbox_events", "processed_events", "purchase_order_facts", "scorecard_observations", "scorecards",
}

// coreTables are the tables of migration 0001; 0002 adds the next three and
// 0003 the two inbound read models.
var coreTables = []string{"purchase_order_facts", "scorecard_observations", "scorecards"}

// withoutInbound is every table before migration 0003.
var withoutInbound = []string{"idempotency_keys", "outbox_events", "processed_events", "purchase_order_facts", "scorecard_observations", "scorecards"}

func tables(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT table_name FROM information_schema.tables
		WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
		ORDER BY table_name COLLATE "C"`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		out = append(out, name)
	}
	return out
}

func wantTables(t *testing.T, pool *pgxpool.Pool, want []string) {
	t.Helper()
	if got := tables(t, pool); !slices.Equal(got, want) {
		t.Fatalf("tables = %v, want %v", got, want)
	}
}

func migrator(t *testing.T, url string) *migrate.Migrate {
	t.Helper()
	m, err := postgres.NewMigrator(url)
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	return m
}

func TestMigrationsUpDownUp(t *testing.T) {
	url := emptyURL(t)
	pool := openPool(t, url)
	wantTables(t, pool, []string{})

	m := migrator(t, url)
	noErr(t, m.Up())
	wantTables(t, pool, allTables)

	noErr(t, m.Steps(-1)) // down 0003
	wantTables(t, pool, withoutInbound)

	noErr(t, m.Steps(-1)) // down 0002
	wantTables(t, pool, coreTables)

	noErr(t, m.Down()) // down 0001
	wantTables(t, pool, []string{})

	noErr(t, m.Up())
	wantTables(t, pool, allTables)
}

func TestRunMigrationsTwiceIsANoOp(t *testing.T) {
	url := emptyURL(t)
	for range 2 {
		noErr(t, postgres.RunMigrations(url))
	}
	wantTables(t, openPool(t, url), allTables)
}
