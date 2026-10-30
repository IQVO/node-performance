package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// now is the fixture's fixed recompute instant (whole seconds, so the wire
// format is exactly what the assertions expect).
var now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// stubReader is an in-memory ports.ScorecardReader over fixture views, the
// double the MCP and REST adapters' tests use. It applies the window filter and
// the keyset cursor the way the Postgres reader does, so paging is exercised
// for real; the grade match itself is baked into the fixture pages.
type stubReader struct {
	nodes map[string][]views.Scorecard
	pages map[string][]views.Scorecard // key: grade
}

func (r *stubReader) ByNode(_ context.Context, nodeID scorecard.NodeID) ([]views.Scorecard, error) {
	return r.nodes[string(nodeID)], nil
}

func (r *stubReader) ByGrade(_ context.Context, f repository.ScorecardFilter) (views.ScorecardPage, error) {
	items := make([]views.Scorecard, 0)
	more := false
	for _, v := range r.pages[f.Grade] {
		if f.Window != "" && v.Window != string(f.Window) {
			continue
		}
		if f.After != "" && v.ScorecardID <= f.After {
			continue
		}
		if len(items) == f.Limit {
			more = true
			break
		}
		items = append(items, v)
	}
	page := views.ScorecardPage{Items: items}
	if more && len(items) > 0 {
		page.NextCursor = items[len(items)-1].ScorecardID
	}
	return page, nil
}

// errBoom is an infrastructure failure whose text must never reach a client.
var errBoom = errors.New("pgx: connection refused to 10.0.0.7:5432 password=hunter2")

// failingReader makes every read fail, standing in for a database outage.
type failingReader struct{}

func (failingReader) ByNode(context.Context, scorecard.NodeID) ([]views.Scorecard, error) {
	return nil, errBoom
}

func (failingReader) ByGrade(context.Context, repository.ScorecardFilter) (views.ScorecardPage, error) {
	return views.ScorecardPage{}, errBoom
}

func gradedA(node string) views.Scorecard {
	return views.Scorecard{
		ScorecardID: node + ":7d", NodeID: node, Window: "7d", State: "GRADED", Grade: "A",
		Metrics: views.Metrics{
			OnTimeHandoverRate: 0.98, LateAckRate: 0.01, RejectRate: 0.005, ExpiryRate: 0.005,
			PromiseKeptRate: 0.97, FeedFreshnessP95Minutes: 38, Observations: 412,
		},
		ComputedAt: now,
	}
}

func provisional(node string) views.Scorecard {
	return views.Scorecard{
		ScorecardID: node + ":30d", NodeID: node, Window: "30d", State: "PROVISIONAL", Grade: "PROVISIONAL",
		Metrics: views.Metrics{Observations: 12}, ComputedAt: now,
	}
}

func fixture() *stubReader {
	return &stubReader{
		nodes: map[string][]views.Scorecard{"N-0042": {gradedA("N-0042"), provisional("N-0042")}},
		pages: map[string][]views.Scorecard{
			"A": {gradedA("N-0042"), gradedA("N-0043"), gradedA("N-0044"), {ScorecardID: "N-0045:30d", NodeID: "N-0045", Window: "30d", State: "GRADED", Grade: "A"}},
		},
	}
}

func depsOver(reader ports.ScorecardReader) Deps {
	q := usecases.Queries{Reader: reader}
	return Deps{GetScorecard: q, ListScorecards: q}
}

// ---- the wire: JSON-RPC over HTTP, driven by the SDK's own client ----------

// wireEnvelope is a TEST-LOCAL copy of the answer contract. If the adapter's
// envelope drifts, this suite fails instead of silently following it.
type wireEnvelope struct {
	Skill string `json:"skill"`
	Data  struct {
		NodePerformance *struct {
			NodeID     string          `json:"nodeId"`
			Scorecards []wireScorecard `json:"scorecards"`
		} `json:"nodePerformance"`
		NodesByGrade *struct {
			Items      *[]wireScorecard `json:"items"`
			NextCursor string           `json:"nextCursor"`
		} `json:"nodesByGrade"`
	} `json:"data"`
	Problem *struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
		Status int    `json:"status"`
	} `json:"problem"`
}

type wireScorecard struct {
	ScorecardID string `json:"scorecardId"`
	NodeID      string `json:"nodeId"`
	Window      string `json:"window"`
	State       string `json:"state"`
	Grade       string `json:"grade"`
	Metrics     struct {
		OnTimeHandoverRate      float64 `json:"onTimeHandoverRate"`
		LateAckRate             float64 `json:"lateAckRate"`
		RejectRate              float64 `json:"rejectRate"`
		ExpiryRate              float64 `json:"expiryRate"`
		PromiseKeptRate         float64 `json:"promiseKeptRate"`
		FeedFreshnessP95Minutes int     `json:"feedFreshnessP95Minutes"`
		Observations            int     `json:"observations"`
	} `json:"metrics"`
	ComputedAt string `json:"computedAt"`
}

type wire struct {
	client *a2aclient.Client
}

func newWire(t *testing.T, deps Deps) *wire {
	t.Helper()
	srv := httptest.NewServer(Handler(NewExecutor(deps)))
	t.Cleanup(srv.Close)
	card := Card(Config{AgentURL: srv.URL + "/", Version: "test"})
	client, err := a2aclient.NewFromCard(context.Background(), card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}
	return &wire{client: client}
}

// send sends one message and returns the agent's reply Message.
func (w *wire) send(t *testing.T, msg *a2a.Message) *a2a.Message {
	t.Helper()
	resp, err := w.client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 2 {
		t.Fatalf("reply = %T, want a message with a data and a text part", resp)
	}
	return reply
}

// call sends one message part and returns the decoded envelope and the
// one-line text summary of the agent's reply.
func (w *wire) call(t *testing.T, part *a2a.Part) (wireEnvelope, string) {
	t.Helper()
	return decodeReply(t, w.send(t, a2a.NewMessage(a2a.MessageRoleUser, part)))
}

func decodeReply(t *testing.T, msg *a2a.Message) (wireEnvelope, string) {
	t.Helper()
	data, ok := msg.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("first reply part is %T, want data", msg.Parts[0].Content)
	}
	raw, _ := json.Marshal(data.Value)
	var env wireEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("reply is not the skill envelope: %v (%s)", err, raw)
	}
	text, _ := msg.Parts[1].Content.(a2a.Text)
	return env, string(text)
}

func (w *wire) skill(t *testing.T, skill string, args map[string]any) (wireEnvelope, string) {
	t.Helper()
	return w.call(t, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
}

func wantProblem(t *testing.T, env wireEnvelope, status int, slug string) {
	t.Helper()
	if env.Problem == nil {
		t.Fatalf("expected a %d problem, got data: %+v", status, env.Data)
	}
	if env.Problem.Status != status || env.Problem.Type != slug {
		t.Fatalf("problem = %+v, want %d %s", env.Problem, status, slug)
	}
}

func wantFirstWindow(t *testing.T, first wireScorecard) {
	t.Helper()
	if first.ScorecardID != "N-0042:7d" || first.State != "GRADED" || first.Grade != "A" || first.ComputedAt != "2026-10-22T12:00:00Z" ||
		first.Metrics.OnTimeHandoverRate != 0.98 || first.Metrics.FeedFreshnessP95Minutes != 38 || first.Metrics.Observations != 412 {
		t.Fatalf("7d scorecard = %+v", first)
	}
}

func wantSecondWindow(t *testing.T, second wireScorecard) {
	t.Helper()
	if second.ScorecardID != "N-0042:30d" || second.State != "PROVISIONAL" || second.Grade != "PROVISIONAL" || second.Metrics.Observations != 12 {
		t.Fatalf("30d scorecard = %+v", second)
	}
}

func TestWireNodePerformanceReturnsBothWindows(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	env, text := w.skill(t, skillNodePerformance, map[string]any{"nodeId": "N-0042"})
	n := env.Data.NodePerformance
	if env.Problem != nil || n == nil || n.NodeID != "N-0042" || len(n.Scorecards) != 2 {
		t.Fatalf("envelope = %+v", env)
	}
	wantFirstWindow(t, n.Scorecards[0])
	wantSecondWindow(t, n.Scorecards[1])
	if want := "N-0042: 7d A (412 observations), 30d PROVISIONAL (12 observations)"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}
}

func TestWireComputedAtIsOmittedBeforeTheFirstRecompute(t *testing.T) {
	notYet := gradedA("N-0042")
	notYet.ComputedAt = time.Time{}
	w := newWire(t, depsOver(&stubReader{nodes: map[string][]views.Scorecard{"N-0042": {notYet}}}))

	env, _ := w.skill(t, skillNodePerformance, map[string]any{"nodeId": "N-0042"})
	if env.Problem != nil || env.Data.NodePerformance == nil || env.Data.NodePerformance.Scorecards[0].ComputedAt != "" {
		t.Fatalf("envelope = %+v, want computedAt omitted", env)
	}
}

func TestWireNodesByGradeFiltersWindowsAndPages(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	all, text := w.skill(t, skillNodesByGrade, map[string]any{"grade": "A"})
	if all.Problem != nil || all.Data.NodesByGrade == nil || all.Data.NodesByGrade.Items == nil || len(*all.Data.NodesByGrade.Items) != 4 {
		t.Fatalf("unfiltered list = %+v", all)
	}
	if want := "4 scorecards at grade A: N-0042:7d, N-0043:7d, N-0044:7d, N-0045:30d"; text != want {
		t.Fatalf("summary = %q, want %q", text, want)
	}

	byWindow, _ := w.skill(t, skillNodesByGrade, map[string]any{"grade": "A", "window": "30d"})
	if items := *byWindow.Data.NodesByGrade.Items; len(items) != 1 || items[0].ScorecardID != "N-0045:30d" {
		t.Fatalf("window filter = %+v", byWindow)
	}

	first, firstText := w.skill(t, skillNodesByGrade, map[string]any{"grade": "A", "limit": 2})
	cursor := first.Data.NodesByGrade.NextCursor
	if len(*first.Data.NodesByGrade.Items) != 2 || cursor != "N-0043:7d" {
		t.Fatalf("first page = %+v", first)
	}
	if !strings.Contains(firstText, "next cursor N-0043:7d") {
		t.Fatalf("summary %q does not advertise the next cursor", firstText)
	}
	second, _ := w.skill(t, skillNodesByGrade, map[string]any{"grade": "A", "limit": 2, "after": cursor})
	items := *second.Data.NodesByGrade.Items
	if len(items) != 2 || second.Data.NodesByGrade.NextCursor != "" || items[0].ScorecardID == (*first.Data.NodesByGrade.Items)[0].ScorecardID {
		t.Fatalf("second page = %+v", second)
	}
}

func TestWireAGradeNobodyHoldsIsAnEmptyPageNotAProblem(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	env, text := w.skill(t, skillNodesByGrade, map[string]any{"grade": "D"})
	if env.Problem != nil || env.Data.NodesByGrade == nil || env.Data.NodesByGrade.Items == nil || len(*env.Data.NodesByGrade.Items) != 0 {
		t.Fatalf("envelope = %+v, want an empty items array", env)
	}
	if text != "0 scorecards at grade D" {
		t.Fatalf("summary = %q", text)
	}
}

// Domain rejections keep the REST adapter's classification: an unknown node is
// 404 not-found, a value the domain refuses is 422 validation-failed. They are
// answered in-band, never raised as JSON-RPC errors (the client would return an
// error from SendMessage otherwise).
func TestWireDomainRejectionsAreAnsweredWithTheRESTClassification(t *testing.T) {
	w := newWire(t, depsOver(fixture()))
	cases := map[string]struct {
		skill  string
		args   map[string]any
		status int
		slug   string
	}{
		"unknown node":        {skillNodePerformance, map[string]any{"nodeId": "N-9999"}, 404, "not-found"},
		"malformed node id":   {skillNodePerformance, map[string]any{"nodeId": "a/b"}, 422, "validation-failed"},
		"node id with colon":  {skillNodePerformance, map[string]any{"nodeId": "N-1:7d"}, 422, "validation-failed"},
		"missing node id":     {skillNodePerformance, map[string]any{}, 422, "validation-failed"},
		"NUL in node id":      {skillNodePerformance, map[string]any{"nodeId": "N-1\u0000"}, 422, "validation-failed"},
		"grade not a grade":   {skillNodesByGrade, map[string]any{"grade": "E"}, 422, "validation-failed"},
		"missing grade":       {skillNodesByGrade, map[string]any{}, 422, "validation-failed"},
		"window not a window": {skillNodesByGrade, map[string]any{"grade": "A", "window": "90d"}, 422, "validation-failed"},
		"limit too large":     {skillNodesByGrade, map[string]any{"grade": "A", "limit": 9999}, 422, "validation-failed"},
		"limit negative":      {skillNodesByGrade, map[string]any{"grade": "A", "limit": -1}, 422, "validation-failed"},
		"cursor too long":     {skillNodesByGrade, map[string]any{"grade": "A", "after": strings.Repeat("x", 81)}, 422, "validation-failed"},
		"cursor with control": {skillNodesByGrade, map[string]any{"grade": "A", "after": "N-1\u0000:7d"}, 422, "validation-failed"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, tc.status, tc.slug)
			if !strings.Contains(text, fmt.Sprintf("(%d)", tc.status)) {
				t.Fatalf("the text summary %q does not carry the status", text)
			}
		})
	}
}

// The not-found text is adapter-owned: the shared use-case error carries an
// internal "not found:" prefix that is not the answer's business.
func TestWireUnknownNodeAnswerIsAdapterOwned(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	env, text := w.skill(t, skillNodePerformance, map[string]any{"nodeId": "N-9999"})
	wantProblem(t, env, 404, "not-found")
	if env.Problem.Detail != `no scorecard exists for node "N-9999"` {
		t.Fatalf("detail = %q", env.Problem.Detail)
	}
	if strings.Contains(text, "not found:") {
		t.Fatalf("the summary leaks the internal error text: %q", text)
	}
}

// An unexpected failure is answered as a 500 problem, never a JSON-RPC error,
// and its raw text (which may carry infrastructure detail) is not echoed.
func TestWireUnexpectedFailureIs500AndDoesNotLeakTheCause(t *testing.T) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	w := newWire(t, depsOver(failingReader{}))

	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"node": {skillNodePerformance, map[string]any{"nodeId": "N-0042"}},
		"list": {skillNodesByGrade, map[string]any{"grade": "A"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, text := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 500, "internal-error")
			for _, s := range []string{env.Problem.Detail, env.Problem.Title, text} {
				if strings.Contains(s, "hunter2") || strings.Contains(s, "10.0.0.7") {
					t.Fatalf("a 500 answer leaks the underlying error: %q", s)
				}
			}
		})
	}
}

// Arguments the strict decoder refuses are a 400 malformed-request-body, the
// REST class of an unknown query parameter or a limit that is not an integer.
func TestWireArgumentValidation(t *testing.T) {
	w := newWire(t, depsOver(fixture()))
	cases := map[string]struct {
		skill string
		args  map[string]any
	}{
		"node: unknown argument":     {skillNodePerformance, map[string]any{"nodeId": "N-0042", "force": true}},
		"node: nodeId wrong type":    {skillNodePerformance, map[string]any{"nodeId": 42}},
		"node: typo'd argument":      {skillNodePerformance, map[string]any{"nodeIdd": "N-0042"}},
		"node: wrong-skill argument": {skillNodePerformance, map[string]any{"nodeId": "N-0042", "grade": "A"}},
		"list: unknown argument":     {skillNodesByGrade, map[string]any{"grade": "A", "windw": "7d"}},
		"list: limit a string":       {skillNodesByGrade, map[string]any{"grade": "A", "limit": "5"}},
		"list: limit fractional":     {skillNodesByGrade, map[string]any{"grade": "A", "limit": 1.5}},
		"list: grade wrong type":     {skillNodesByGrade, map[string]any{"grade": 1}},
		"list: wrong-skill argument": {skillNodesByGrade, map[string]any{"grade": "A", "nodeId": "N-0042"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			env, _ := w.skill(t, tc.skill, tc.args)
			wantProblem(t, env, 400, "malformed-request-body")
		})
	}
}

func TestWireRequestShapeProblems(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	env, _ := w.call(t, a2a.NewTextPart("not json at all"))
	wantProblem(t, env, 400, "malformed-request-body")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"arguments": map[string]any{}}))
	wantProblem(t, env, 400, "malformed-request-body")

	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillNodesByGrade, "arguments": map[string]any{"grade": "A"}, "extra": 1}))
	wantProblem(t, env, 400, "malformed-request-body")

	// Trailing data after the JSON document is refused too.
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"nodes_by_grade","arguments":{"grade":"A"}} {"skill":"node_performance"}`))
	wantProblem(t, env, 400, "malformed-request-body")

	// ...and after the arguments object.
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"node_performance","arguments":{"nodeId":"N-0042"} }  []`))
	wantProblem(t, env, 400, "malformed-request-body")

	// A file part is not a skill request.
	env, _ = w.call(t, a2a.NewRawPart([]byte("x")))
	wantProblem(t, env, 400, "malformed-request-body")

	// A text part holding the same JSON is accepted (documented contract).
	env, _ = w.call(t, a2a.NewTextPart(`{"skill":"node_performance","arguments":{"nodeId":"N-0042"}}`))
	if env.Problem != nil || env.Data.NodePerformance == nil || env.Data.NodePerformance.NodeID != "N-0042" {
		t.Fatalf("a text part holding the skill JSON must be accepted, got %+v", env)
	}

	// Absent arguments reach the use case, which names the missing value.
	env, _ = w.call(t, a2a.NewDataPart(map[string]any{"skill": skillNodePerformance}))
	wantProblem(t, env, 422, "validation-failed")
}

// This context's MCP charter is read-only: every write verb is an unknown
// skill, answered in-band.
func TestWireOffersNoWriteSkill(t *testing.T) {
	w := newWire(t, depsOver(fixture()))

	for _, skill := range []string{"record_observation", "recompute_scorecards", "publish_scorecard_updated", "set_threshold", "launch_rocket"} {
		env, _ := w.skill(t, skill, map[string]any{"nodeId": "N-0042"})
		wantProblem(t, env, 400, "unknown-skill")
	}
}

// The reply carries the caller's context so a delegating saga can group its
// interactions.
func TestWireReplyKeepsTheCallersContext(t *testing.T) {
	w := newWire(t, depsOver(fixture()))
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skillNodePerformance, "arguments": map[string]any{"nodeId": "N-0042"}}))
	msg.ContextID = "saga-7"

	if got := w.send(t, msg).ContextID; got != "saga-7" {
		t.Fatalf("reply contextId = %q, want saga-7", got)
	}
}

// TestCardIsServedAndDialable proves discovery end to end: the card handler
// serves the card at the well-known path and a client built from the FETCHED
// card reaches the agent.
func TestCardIsServedAndDialable(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("/", Handler(NewExecutor(depsOver(fixture()))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(Card(Config{AgentURL: srv.URL + "/", Version: "test"})))

	res, err := http.Get(srv.URL + a2asrv.WellKnownAgentCardPath)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("card fetch = %v / %v", res, err)
	}
	defer func() { _ = res.Body.Close() }()
	var card a2a.AgentCard
	if err := json.NewDecoder(res.Body).Decode(&card); err != nil {
		t.Fatal(err)
	}
	client, err := a2aclient.NewFromCard(context.Background(), &card)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := (&wire{client: client}).skill(t, skillNodePerformance, map[string]any{"nodeId": "N-0042"})
	if env.Data.NodePerformance == nil {
		t.Fatalf("a client built from the served card could not call the agent: %+v", env)
	}
}

// One skill run yields one span named "a2a.skill <id>" carrying the a2a.skill
// attribute; a problem answer marks it as an error, a success does not.
func TestEachSkillRunIsOneSpanWithTheSkillAttribute(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })
	recorder := tracetest.NewSpanRecorder()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	w := newWire(t, depsOver(fixture()))

	w.skill(t, skillNodePerformance, map[string]any{"nodeId": "N-0042"})
	w.skill(t, skillNodePerformance, map[string]any{"nodeId": "N-9999"})

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want one per skill run", len(spans))
	}
	for i, wantError := range []bool{false, true} {
		span := spans[i]
		if span.Name() != "a2a.skill node_performance" {
			t.Errorf("span %d name = %q", i, span.Name())
		}
		var attr string
		for _, kv := range span.Attributes() {
			if string(kv.Key) == "a2a.skill" {
				attr = kv.Value.AsString()
			}
		}
		if attr != skillNodePerformance {
			t.Errorf("span %d a2a.skill = %q", i, attr)
		}
		if isError := span.Status().Code == codes.Error; isError != wantError {
			t.Errorf("span %d status = %v, error wanted: %v", i, span.Status(), wantError)
		}
	}
}
