//go:build integration

package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	// The pool below is a pgx pool: this fleet's rule (and cmd/api's own
	// integration test) requires the testcontainers postgres import alongside
	// it in this package; the database itself comes from internal/testing/pgtest.
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	inboundmcp "github.com/claudioed/node-performance/internal/adapters/inbound/mcp"
	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// now is the fixed recompute instant of the seeded world.
var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// The binary migrates a database itself (idempotently: pgtest's databases
// are already migrated, a second run must be a no-op), then reads through the
// MCP tools exactly what the write path persisted, and its reads add no
// outbox row.
func TestMCPAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.FreshURL(t) // a fresh, already migrated database
	ctx := context.Background()

	seedPool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer seedPool.Close()
	countOutbox := func() int {
		t.Helper()
		var n int
		if err := seedPool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&n); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return n
	}

	seedThroughTheWritePath(t, ctx, seedPool)
	if before := countOutbox(); before != 0 {
		t.Fatalf("outbox rows after seeding = %d, want 0 (the repository save path publishes nothing)", before)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	ct, st := sdk.NewInMemoryTransports()
	ss, err := inboundmcp.NewServer(deps).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	session, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertSeededReads(t, session)

	if after := countOutbox(); after != 0 {
		t.Fatalf("outbox rows 0 -> %d: the MCP binary wrote an event", after)
	}
}

// seedThroughTheWritePath stores N-0042's two scorecards through the SAME
// repository the cmd/api write path uses (the intake and recompute use cases
// call ScorecardRepository.Save under their unit of work): 50 on-time
// handovers in the 7-day window, recomputed, grade A; 12 in the 30-day
// window, left PROVISIONAL below the observation minimum.
func seedThroughTheWritePath(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	repo := postgres.NewScorecardRepo(pool)

	node, err := scorecard.NewNodeID("N-0042")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []struct {
		window scorecard.Window
		count  int
	}{
		{scorecard.Window7d, scorecard.MinObservations},
		{scorecard.Window30d, 12},
	} {
		s := openWith(t, node, w.window)
		if err := repo.Save(ctx, s); err != nil {
			t.Fatalf("save %s scorecard: %v", w.window, err)
		}
		// One save per observation, the way the service writes it: Save
		// expects the stored version to be aggregate.Version()-1.
		for i := 1; i < w.count; i++ {
			if err := s.RecordObservation(handoverOf(t, node, w.window, i)); err != nil {
				t.Fatalf("record observation %d: %v", i, err)
			}
			if err := repo.Save(ctx, s); err != nil {
				t.Fatalf("save %s scorecard: %v", w.window, err)
			}
		}
		if w.count >= scorecard.MinObservations {
			s.Recompute(now)
			if err := repo.Save(ctx, s); err != nil {
				t.Fatalf("save the recomputed %s scorecard: %v", w.window, err)
			}
		}
	}
}

// handoverOf builds the i-th on-time handover of (node, window).
func handoverOf(t *testing.T, node scorecard.NodeID, w scorecard.Window, i int) scorecard.Observation {
	t.Helper()
	at := now.Add(time.Duration(i) * time.Minute)
	o, err := scorecard.NewHandover(string(node)+"-"+string(w)+"-"+time.Duration(i).String(), at, at.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewHandover: %v", err)
	}
	return o
}

// openWith opens the scorecard of (node, window) with its first observation.
func openWith(t *testing.T, node scorecard.NodeID, w scorecard.Window) *scorecard.NodeScorecard {
	t.Helper()
	s, err := scorecard.Open(node, w, handoverOf(t, node, w, 0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// assertSeededReads reads back, through both MCP tools, what
// seedThroughTheWritePath persisted.
func assertSeededReads(t *testing.T, session *sdk.ClientSession) {
	t.Helper()
	ctx := context.Background()
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: err=%v res=%+v", name, err, res)
		}
		return res.StructuredContent.(map[string]any)
	}

	node := call("get_scorecard", map[string]any{"nodeId": "N-0042"})
	if node["nodeId"] != "N-0042" {
		t.Fatalf("get_scorecard = %v", node)
	}
	cards := node["scorecards"].([]any)
	if len(cards) != 2 {
		t.Fatalf("get_scorecard returned %d scorecards, want 2 (7d and 30d)", len(cards))
	}
	first := cards[0].(map[string]any)
	if first["scorecardId"] != "N-0042:7d" || first["state"] != "GRADED" || first["grade"] != "A" {
		t.Fatalf("the 7d scorecard = %v", first)
	}
	metrics := first["metrics"].(map[string]any)
	if metrics["onTimeHandoverRate"] != 1.0 || metrics["observations"] != 50.0 {
		t.Fatalf("the 7d metrics = %v", metrics)
	}
	if first["computedAt"] != now.Format(time.RFC3339) {
		t.Fatalf("computedAt = %v", first["computedAt"])
	}
	second := cards[1].(map[string]any)
	if second["scorecardId"] != "N-0042:30d" || second["grade"] != "PROVISIONAL" {
		t.Fatalf("the 30d scorecard = %v", second)
	}

	page := call("list_scorecards_by_grade", map[string]any{"grade": "A"})
	items := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["scorecardId"] != "N-0042:7d" {
		t.Fatalf("grade A page = %v", page)
	}
	if _, hasCursor := page["nextCursor"]; hasCursor {
		t.Fatalf("the one-row page must be the last: %v", page)
	}
	empty := call("list_scorecards_by_grade", map[string]any{"grade": "D"})
	if got := len(empty["items"].([]any)); got != 0 {
		t.Fatalf("grade D = %d items, want 0", got)
	}

	// A node nothing was observed about is not-found, the REST contract's 404.
	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name: "get_scorecard", Arguments: map[string]any{"nodeId": "N-9999"},
	})
	if err != nil || !res.IsError {
		t.Fatalf("get_scorecard of an unknown node = err %v res %+v, want a tool error", err, res)
	}
}
