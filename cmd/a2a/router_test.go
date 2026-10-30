package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	inbounda2a "github.com/claudioed/node-performance/internal/adapters/inbound/a2a"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// seededReader holds one node with a GRADED 7-day scorecard. It is a test
// double for ports.ScorecardReader; the binary wires Postgres.
type seededReader struct{}

func (seededReader) ByNode(_ context.Context, id scorecard.NodeID) ([]views.Scorecard, error) {
	if id != "N-0042" {
		return nil, nil
	}
	return []views.Scorecard{{
		ScorecardID: "N-0042:7d", NodeID: "N-0042", Window: "7d", State: "GRADED", Grade: "A",
		Metrics:    views.Metrics{Observations: 412},
		ComputedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	}}, nil
}

func (seededReader) ByGrade(context.Context, repository.ScorecardFilter) (views.ScorecardPage, error) {
	return views.ScorecardPage{Items: []views.Scorecard{}}, nil
}

// newTestAgent builds the router exactly as run() does (newAgent), over the
// real read use cases on the seeded reader.
func newTestAgent() http.Handler {
	q := usecases.Queries{Reader: seededReader{}}
	return newAgent(inbounda2a.Deps{GetScorecard: q, ListScorecards: q}, testAgentURL, "test", quietLogger(), "node-performance-a2a")
}

func TestRouter_HealthzIsOpenAndServesOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestAgent().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK || rec.Body.String() != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %q, want 200 {\"status\":\"ok\"}", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func TestRouter_ServesTheAgentCardAtTheWellKnownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestAgent().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET agent card: got %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"node-performance"`, testAgentURL, `"node_performance"`, `"nodes_by_grade"`} {
		if !strings.Contains(body, want) {
			t.Errorf("agent card lacks %s: %s", want, body)
		}
	}
}

// TestRouter_BothMountsReachTheJSONRPCHandler pins the fleet's
// <CTX>_A2A_ENDPOINT convention: the endpoint answers at "/" and at "/a2a",
// and the use cases behind it are the wired ones (the seeded node is read).
func TestRouter_BothMountsReachTheJSONRPCHandler(t *testing.T) {
	h := newTestAgent()
	for _, path := range []string{"/", "/a2a"} {
		t.Run(path, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"node_performance","arguments":{"nodeId":"N-0042"}}}]}}}`
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("A2A-Version", "1.0")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			raw, _ := io.ReadAll(rec.Body)
			if rec.Code == http.StatusNotFound || !strings.Contains(string(raw), `"jsonrpc"`) {
				t.Fatalf("POST %s: %d %q does not look like a JSON-RPC reply", path, rec.Code, raw)
			}
			if !strings.Contains(string(raw), `"scorecardId":"N-0042:7d"`) {
				t.Fatalf("POST %s: reply %q lacks the seeded scorecard read through the wired use case", path, raw)
			}
		})
	}
}

// TestRouter_ACardFetchedFromTheBinaryIsDialable runs the discovery flow a
// delegating agent runs against the real router: fetch the card, build a
// client from it, call a skill.
func TestRouter_ACardFetchedFromTheBinaryIsDialable(t *testing.T) {
	srv := httptest.NewServer(newTestAgent())
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	// The card advertises the configured URL, not the test server's: point the
	// client at the test server the way a gateway-rewritten route would.
	card.SupportedInterfaces[0].URL = srv.URL + "/a2a"
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": "node_performance", "arguments": map[string]any{"nodeId": "N-0042"}}))})
	if err != nil {
		t.Fatal(err)
	}
	msg, ok := resp.(*a2a.Message)
	if !ok || len(msg.Parts) < 2 {
		t.Fatalf("reply = %T", resp)
	}
	if text, _ := msg.Parts[1].Content.(a2a.Text); !strings.Contains(string(text), "N-0042: 7d A") {
		t.Fatalf("summary = %q, want the seeded node", text)
	}
}

// The binary reads no API key: requests carrying (or lacking) any
// Authorization header behave identically.
func TestNoAuthIsRequired(t *testing.T) {
	h := newTestAgent()
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"data":{"skill":"node_performance","arguments":{"nodeId":"N-0042"}}}]}}}`
	for _, auth := range []string{"", "Bearer nonsense"} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("A2A-Version", "1.0")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"scorecardId":"N-0042:7d"`) {
			t.Fatalf("SendMessage with Authorization=%q = %d %s, want the answer", auth, rec.Code, rec.Body.String())
		}
	}
}
