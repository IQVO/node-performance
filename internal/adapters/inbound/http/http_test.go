package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	inboundhttp "github.com/claudioed/node-performance/internal/adapters/inbound/http"
	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

var now = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

// seeded is the real query use cases over a memory store holding node-a with
// 50 on-time handovers (graded A) and node-b with one feed (PROVISIONAL).
func seeded(t *testing.T) usecases.Queries {
	t.Helper()
	s := memory.NewStore()
	d := usecases.Deps{Scorecards: s.Scorecards(), Facts: s.Facts(), Inbound: s.Inbound(), Inbox: s.Inbox(), Outbox: s.Outbox(), UoW: s.UnitOfWork(), Clock: memory.NewClock(now)}
	in := usecases.NewIntake(d)
	ctx := context.Background()
	ref := func(id string) usecases.EventRef { return usecases.EventRef{ID: id, Type: "x", Time: now} }
	for i := 0; i < 50; i++ {
		po := fmt.Sprintf("po-%d", i)
		must(t, firstErr(in.PurchaseOrderIssued(ctx, usecases.PurchaseOrderIssued{Ref: ref("i" + po), PoNumber: po, NodeID: "node-a", RequiredShipBy: now.Add(time.Hour), AckBy: now.Add(time.Hour)})))
		must(t, firstErr(in.PackageHandedOver(ctx, usecases.PackageHandedOver{Ref: ref("h" + po), PoNumber: po, NodeID: "node-a", HandedOverAt: now})))
	}
	must(t, firstErr(in.InventoryFeedApplied(ctx, usecases.InventoryFeedApplied{Ref: ref("f1"), NodeID: "node-b", AppliedAt: now})))
	if _, err := usecases.NewRecompute(d).Run(ctx); err != nil {
		t.Fatal(err)
	}
	return usecases.Queries{Reader: s.Reader()}
}

func firstErr(_ usecases.Outcome, err error) error { return err }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func router(t *testing.T, s *inboundhttp.Server) http.Handler {
	t.Helper()
	h, err := inboundhttp.NewRouter(s)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type reply struct {
	status int
	header http.Header
	raw    string
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.raw), &m); err != nil {
		t.Fatalf("not JSON: %q", r.raw)
	}
	return m
}

func (r reply) problem(t *testing.T) (slug string) {
	t.Helper()
	if ct := r.header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q, body %s", ct, r.raw)
	}
	m := r.json(t)
	if int(m["status"].(float64)) != r.status || m["title"] == "" || m["type"] == "" {
		t.Fatalf("incomplete problem %v", m)
	}
	return m["type"].(string)
}

func get(h http.Handler, target string) reply {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path, req.URL.RawQuery = target, ""
	if i := strings.IndexByte(target, '?'); i >= 0 {
		req.URL.Path, req.URL.RawQuery = target[:i], target[i+1:]
	}
	req.RequestURI = target
	return serve(h, req)
}

func serve(h http.Handler, req *http.Request) reply {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return reply{status: rec.Code, header: rec.Header(), raw: rec.Body.String()}
}

func realServer(t *testing.T) *inboundhttp.Server {
	q := seeded(t)
	return &inboundhttp.Server{GetScorecard: q, ListScorecards: q}
}

func TestGetScorecardReturnsBothWindowsInTheContractShape(t *testing.T) {
	r := get(router(t, realServer(t)), "/scorecards/node-a")
	if r.status != 200 || r.header.Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	body := r.json(t)
	list := body["scorecards"].([]any)
	if body["nodeId"] != "node-a" || len(list) != 2 {
		t.Fatalf("body = %v", body)
	}
	first := list[0].(map[string]any)
	if first["scorecardId"] != "node-a:30d" && first["scorecardId"] != "node-a:7d" {
		t.Fatalf("scorecardId = %v", first["scorecardId"])
	}
	for _, key := range []string{"scorecardId", "nodeId", "window", "state", "grade", "metrics", "computedAt"} {
		if _, ok := first[key]; !ok {
			t.Errorf("scorecard lacks %s: %v", key, first)
		}
	}
	m := first["metrics"].(map[string]any)
	if len(m) != 7 || first["grade"] != "A" || first["state"] != "GRADED" || m["observations"].(float64) != 50 || m["onTimeHandoverRate"].(float64) != 1 {
		t.Fatalf("scorecard = %v", first)
	}
}

func TestAProvisionalScorecardReadsAsProvisionalWithoutAGradeLetter(t *testing.T) {
	r := get(router(t, realServer(t)), "/scorecards/node-b")
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	first := r.json(t)["scorecards"].([]any)[0].(map[string]any)
	if first["state"] != "PROVISIONAL" || first["grade"] != "PROVISIONAL" {
		t.Fatalf("scorecard = %v", first)
	}
}

func TestAnUnknownNodeIsA404ProblemAndABadIdIs422(t *testing.T) {
	h := router(t, realServer(t))
	if r := get(h, "/scorecards/node-zzz"); r.status != 404 || r.problem(t) != "not-found" {
		t.Fatalf("unknown node = %d %s", r.status, r.raw)
	}
	if r := get(h, "/scorecards/"+strings.Repeat("x", 100)); r.status != 422 || r.problem(t) != "validation-failed" {
		t.Fatalf("overlong id = %d %s", r.status, r.raw)
	}
}

func TestListByGradeFiltersPagesAndValidates(t *testing.T) {
	h := router(t, realServer(t))
	r := get(h, "/scorecards?grade=A")
	items := r.json(t)["items"].([]any)
	if r.status != 200 || len(items) != 2 {
		t.Fatalf("grade A = %d %s", r.status, r.raw)
	}
	if _, has := r.json(t)["nextCursor"]; has {
		t.Fatal("the last page has no nextCursor")
	}
	if r := get(h, "/scorecards?grade=A&window=7d"); len(r.json(t)["items"].([]any)) != 1 {
		t.Fatalf("window filter: %s", r.raw)
	}
	page1 := get(h, "/scorecards?grade=PROVISIONAL&limit=1")
	cursor, _ := page1.json(t)["nextCursor"].(string)
	if cursor == "" || len(page1.json(t)["items"].([]any)) != 1 {
		t.Fatalf("first page = %s", page1.raw)
	}
	page2 := get(h, "/scorecards?grade=PROVISIONAL&limit=1&after="+cursor)
	if page2.status != 200 || len(page2.json(t)["items"].([]any)) != 1 {
		t.Fatalf("second page = %s", page2.raw)
	}
	if r := get(h, "/scorecards?grade=D"); r.status != 200 || r.json(t)["items"] == nil || len(r.json(t)["items"].([]any)) != 0 {
		t.Fatalf("an empty list is an empty array: %d %s", r.status, r.raw)
	}
}

func TestListByGradeRefusesWhatTheContractRefuses(t *testing.T) {
	h := router(t, realServer(t))
	for _, bad := range []string{"/scorecards", "/scorecards?grade=E", "/scorecards?grade=A&window=1d", "/scorecards?grade=A&limit=0", "/scorecards?grade=A&limit=501"} {
		if r := get(h, bad); r.status != 422 || r.problem(t) != "validation-failed" {
			t.Errorf("%s = %d %s, want 422 validation-failed", bad, r.status, r.raw)
		}
	}
	for _, bad := range []string{"/scorecards?grade=A&limit=ten", "/scorecards?grade=A&colour=red"} {
		if r := get(h, bad); r.status != 400 || r.problem(t) != "malformed-request-query" {
			t.Errorf("%s = %d %s, want 400 malformed-request-query", bad, r.status, r.raw)
		}
	}
}

func TestUnknownRoutesAndMethodsAreProblems(t *testing.T) {
	h := router(t, realServer(t))
	if r := get(h, "/nope"); r.status != 404 || r.problem(t) != "not-found" {
		t.Fatalf("unknown route = %d %s", r.status, r.raw)
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/scorecards/node-a", strings.NewReader("{}"))
		if r := serve(h, req); r.status != 405 || r.problem(t) != "method-not-allowed" {
			t.Errorf("%s = %d %s, want 405", method, r.status, r.raw)
		}
	}
}

func TestNULIsRejectedBeforeTheUseCase(t *testing.T) {
	h := router(t, realServer(t))
	for _, tc := range []struct {
		name, target string
		status       int
		slug         string
	}{
		{"encoded NUL in the path", "/scorecards/no%00de", 404, "not-found"},
		{"encoded NUL in the query", "/scorecards?grade=%00", 400, "malformed-request-query"},
		{"raw NUL in the query", "/scorecards?grade=a\x00b", 400, "malformed-request-query"},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.URL.Path, req.URL.RawQuery = tc.target, ""
		if i := strings.IndexByte(tc.target, '?'); i >= 0 {
			req.URL.Path, req.URL.RawQuery = tc.target[:i], tc.target[i+1:]
		}
		if strings.Contains(tc.target, "%00") && !strings.Contains(tc.target, "?") {
			req.URL.Path, req.URL.RawPath = "/scorecards/no\x00de", tc.target
		}
		if r := serve(h, req); r.status != tc.status || r.problem(t) != tc.slug {
			t.Errorf("%s = %d %s, want %d %s", tc.name, r.status, r.raw, tc.status, tc.slug)
		}
	}
	body := httptest.NewRequest(http.MethodGet, "/scorecards/node-a", strings.NewReader(`{"a":"\u0000"}`))
	if r := serve(h, body); r.status != 400 || r.problem(t) != "malformed-request-body" {
		t.Errorf("NUL in a body = %d %s", r.status, r.raw)
	}
	clean := httptest.NewRequest(http.MethodGet, "/scorecards/node-a", strings.NewReader(`{"a":"b"}`))
	if r := serve(h, clean); r.status != 200 {
		t.Errorf("a clean body is passed through: %d %s", r.status, r.raw)
	}
}

type stubQueries struct {
	err  error
	pnic bool
}

func (s stubQueries) GetScorecard(context.Context, string) (views.NodeScorecards, error) {
	if s.pnic {
		panic("boom")
	}
	return views.NodeScorecards{}, s.err
}

func (s stubQueries) ListScorecardsByGrade(context.Context, usecases.ListByGradeInput) (views.ScorecardPage, error) {
	return views.ScorecardPage{}, s.err
}

func TestErrorsMapToTheProblemCatalogue(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		slug   string
	}{
		{"not found", fmt.Errorf("wrapped: %w", ports.ErrNotFound), 404, "not-found"},
		{"concurrent update", ports.ErrConcurrentUpdate, 409, "concurrent-update"},
		{"validation", shared.Invalid("bad"), 422, "validation-failed"},
		{"anything else", errors.New("connection refused: secret-host:5432"), 500, "internal-error"},
	} {
		q := stubQueries{err: tc.err}
		h := router(t, &inboundhttp.Server{GetScorecard: q, ListScorecards: q})
		r := get(h, "/scorecards/n1")
		if r.status != tc.status || r.problem(t) != tc.slug {
			t.Errorf("%s = %d %s", tc.name, r.status, r.raw)
		}
		if tc.status == 500 && strings.Contains(r.raw, "secret-host") {
			t.Errorf("an internal error leaks its message: %s", r.raw)
		}
		if r := get(h, "/scorecards?grade=A"); r.status != tc.status {
			t.Errorf("%s on the list = %d", tc.name, r.status)
		}
	}
}

func TestAPanicIsA500Problem(t *testing.T) {
	q := stubQueries{pnic: true}
	r := get(router(t, &inboundhttp.Server{GetScorecard: q, ListScorecards: q}), "/scorecards/n1")
	if r.status != 500 || r.problem(t) != "internal-error" || strings.Contains(r.raw, "boom") {
		t.Fatalf("%d %s", r.status, r.raw)
	}
}

func TestNewRouterRefusesAServerWithoutItsUseCases(t *testing.T) {
	for _, s := range []*inboundhttp.Server{{}, {GetScorecard: stubQueries{}}, {ListScorecards: stubQueries{}}} {
		if _, err := inboundhttp.NewRouter(s); !errors.Is(err, inboundhttp.ErrIncompleteServer) {
			t.Errorf("err = %v", err)
		}
	}
}

func TestProbes(t *testing.T) {
	var failing error
	gate := inboundhttp.NewReadiness(time.Millisecond, time.Second,
		inboundhttp.Dependency{Name: "postgres", Check: func(context.Context) error { return nil }},
		inboundhttp.Dependency{Name: "kafka", Check: func(context.Context) error { return failing }})
	q := stubQueries{}
	h := router(t, &inboundhttp.Server{GetScorecard: q, ListScorecards: q, Readiness: gate})

	if r := get(h, "/healthz"); r.status != 200 || r.json(t)["status"] != "ok" {
		t.Fatalf("healthz = %d %s", r.status, r.raw)
	}
	if r := get(h, "/readyz"); r.status != 200 || r.json(t)["status"] != "ready" {
		t.Fatalf("readyz = %d %s", r.status, r.raw)
	}
	failing = errors.New("no broker")
	time.Sleep(5 * time.Millisecond)
	if r := get(h, "/readyz"); r.status != 503 || r.problem(t) != "dependency-unavailable" || !strings.Contains(r.raw, "kafka") || strings.Contains(r.raw, "postgres") {
		t.Fatalf("a missing dependency = %d %s", r.status, r.raw)
	}
	failing = nil
	gate.SetNotReady()
	if r := get(h, "/readyz"); r.status != 503 || r.json(t)["status"] != "not_ready" {
		t.Fatalf("draining = %d %s", r.status, r.raw)
	}
	if r := get(h, "/healthz"); r.status != 200 {
		t.Fatalf("liveness never flips on shutdown: %d", r.status)
	}
	var none *inboundhttp.Readiness
	none.SetNotReady()
	if !none.Ready() || none.Failing(context.Background()) != nil {
		t.Fatal("a nil gate is ready")
	}
}

func TestReadinessCachesItsVerdictWithinTheTTL(t *testing.T) {
	calls := 0
	gate := inboundhttp.NewReadiness(0, 0, inboundhttp.Dependency{Name: "d", Check: func(context.Context) error { calls++; return nil }})
	for range 5 {
		_ = gate.Failing(context.Background())
	}
	if calls != 1 {
		t.Fatalf("the default ttl caches the verdict: %d checks", calls)
	}
}
