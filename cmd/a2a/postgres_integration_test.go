//go:build integration

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/jackc/pgx/v5/pgxpool"
	// The pool below is a pgx pool: this fleet's rule (and cmd/api's own
	// integration test) requires the testcontainers postgres import alongside
	// it in this package; the database itself comes from internal/testing/pgtest.
	_ "github.com/testcontainers/testcontainers-go/modules/postgres"

	inbounda2a "github.com/claudioed/node-performance/internal/adapters/inbound/a2a"
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

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then answers over the real
// A2A JSON-RPC wire exactly what the write path persisted, and its reads add no
// outbox row: the agent is read-only, so nothing it does can leave a fact.
func TestA2AAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
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

	srv := httptest.NewServer(newAgent(deps, testAgentURL, "test", quietLogger(), "node-performance-a2a"))
	t.Cleanup(srv.Close)
	card := inboundCard(srv.URL + "/")
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatal(err)
	}

	assertSeededReads(t, client)

	if after := countOutbox(); after != 0 {
		t.Fatalf("outbox rows 0 -> %d: the A2A binary wrote an event", after)
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

// inboundCard is the served Agent Card with its URL pointed at the test server.
func inboundCard(url string) *a2a.AgentCard {
	return inbounda2a.Card(inbounda2a.Config{AgentURL: url, Version: "test"})
}

// ask sends one skill request and returns the envelope's JSON object.
func ask(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("%s: reply = %T", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T", skill, reply.Parts[0].Content)
	}
	env, ok := data.Value.(map[string]any)
	if !ok {
		t.Fatalf("%s: envelope = %#v", skill, data.Value)
	}
	return env
}

// assertSeededReads reads back, through both skills, what
// seedThroughTheWritePath persisted.
func assertSeededReads(t *testing.T, client *a2aclient.Client) {
	t.Helper()

	env := ask(t, client, "node_performance", map[string]any{"nodeId": "N-0042"})
	node, _ := env["data"].(map[string]any)["nodePerformance"].(map[string]any)
	cards, _ := node["scorecards"].([]any)
	if node["nodeId"] != "N-0042" || len(cards) != 2 {
		t.Fatalf("node_performance = %v", env)
	}
	first := cards[0].(map[string]any)
	if first["scorecardId"] != "N-0042:7d" || first["state"] != "GRADED" || first["grade"] != "A" || first["computedAt"] != now.Format(time.RFC3339) {
		t.Fatalf("the 7d scorecard = %v", first)
	}
	if m := first["metrics"].(map[string]any); m["onTimeHandoverRate"] != 1.0 || m["observations"] != 50.0 {
		t.Fatalf("the 7d metrics = %v", m)
	}
	if second := cards[1].(map[string]any); second["scorecardId"] != "N-0042:30d" || second["grade"] != "PROVISIONAL" {
		t.Fatalf("the 30d scorecard = %v", second)
	}

	env = ask(t, client, "nodes_by_grade", map[string]any{"grade": "A"})
	page, _ := env["data"].(map[string]any)["nodesByGrade"].(map[string]any)
	items, _ := page["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["scorecardId"] != "N-0042:7d" {
		t.Fatalf("grade A page = %v", env)
	}
	if _, hasCursor := page["nextCursor"]; hasCursor {
		t.Fatalf("the one-row page must be the last: %v", page)
	}
	env = ask(t, client, "nodes_by_grade", map[string]any{"grade": "D"})
	if got := env["data"].(map[string]any)["nodesByGrade"].(map[string]any)["items"].([]any); len(got) != 0 {
		t.Fatalf("grade D = %d items, want 0", len(got))
	}

	// A node nothing was observed about is the REST contract's 404, answered in-band.
	env = ask(t, client, "node_performance", map[string]any{"nodeId": "N-9999"})
	if p, _ := env["problem"].(map[string]any); p == nil || p["status"] != 404.0 || p["type"] != "not-found" {
		t.Fatalf("an unknown node = %v, want a 404 not-found problem", env)
	}
}
