package mcp_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/application/views"
)

// nodeFixture is the reader of a node with a GRADED 7d scorecard and a
// PROVISIONAL 30d one, the shape GetScorecard returns.
func nodeFixture() *stubReader {
	return &stubReader{
		nodes: map[string][]views.Scorecard{`N-0042`: {gradedA(`N-0042`), provisional(`N-0042`)}},
	}
}

func TestGetScorecard(t *testing.T) {
	h := newHarness(t, nodeFixture())
	got := h.ok(t, "get_scorecard", map[string]any{"nodeId": "N-0042"})
	want := map[string]any{
		"nodeId": "N-0042",
		"scorecards": []any{
			map[string]any{
				"scorecardId": "N-0042:7d", "nodeId": "N-0042", "window": "7d", "state": "GRADED", "grade": "A",
				"metrics": map[string]any{
					"onTimeHandoverRate": 0.98, "lateAckRate": 0.01, "rejectRate": 0.005, "expiryRate": 0.005,
					"promiseKeptRate": 0.97, "feedFreshnessP95Minutes": 38.0, "observations": 412.0,
				},
				"computedAt": "2026-10-22T12:00:00Z",
			},
			map[string]any{
				"scorecardId": "N-0042:30d", "nodeId": "N-0042", "window": "30d", "state": "PROVISIONAL",
				"grade": "PROVISIONAL",
				"metrics": map[string]any{
					"onTimeHandoverRate": 0.0, "lateAckRate": 0.0, "rejectRate": 0.0, "expiryRate": 0.0,
					"promiseKeptRate": 0.0, "feedFreshnessP95Minutes": 0.0, "observations": 12.0,
				},
				"computedAt": "2026-10-22T12:00:00Z",
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("get_scorecard =\n%#v\nwant\n%#v", got, want)
	}
}

func TestGetScorecardOmitsComputedAtBeforeTheFirstRecompute(t *testing.T) {
	notYet := gradedA(`N-0042`)
	notYet.ComputedAt = time.Time{}
	h := newHarness(t, &stubReader{nodes: map[string][]views.Scorecard{`N-0042`: {notYet}}})
	got := h.ok(t, "get_scorecard", map[string]any{"nodeId": "N-0042"})
	first := got["scorecards"].([]any)[0].(map[string]any)
	if _, present := first["computedAt"]; present {
		t.Fatalf("computedAt must be omitted before the first recompute: %v", first)
	}
}

func TestGetScorecardErrors(t *testing.T) {
	h := newHarness(t, nodeFixture())
	h.failWith(t, "get_scorecard", map[string]any{"nodeId": "N-9999"}, "not-found")
	h.failWith(t, "get_scorecard", map[string]any{"nodeId": "a/b"}, "validation-failed")
}

func TestListScorecardsByGradeFiltersWindowsAndPages(t *testing.T) {
	reader := &stubReader{pages: map[string][]views.Scorecard{
		// Grade A holds three 7d scorecards and one 30d one.
		"A": {gradedA(`N-0042`), gradedA(`N-0043`), gradedA(`N-0044`), gradedA(`N-0045`)},
	}}
	// The window of the last one is rewritten so the window filter has an
	// effect: N-0043 and N-0044 stay 7d, N-0045 becomes 30d.
	items := reader.pages["A"]
	items[3] = views.Scorecard{ScorecardID: "N-0045:30d", NodeID: "N-0045", Window: "30d", State: "GRADED", Grade: "A"}
	reader.pages["A"] = items

	h := newHarness(t, reader)
	all := h.ok(t, "list_scorecards_by_grade", map[string]any{"grade": "A"})
	if got := len(all["items"].([]any)); got != 4 {
		t.Fatalf("unfiltered list = %d items, want 4: %v", got, all)
	}
	byWindow := h.ok(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "window": "30d"})
	if got := len(byWindow["items"].([]any)); got != 1 || byWindow["items"].([]any)[0].(map[string]any)["scorecardId"] != "N-0045:30d" {
		t.Fatalf("window filter = %v", byWindow)
	}
	empty := h.ok(t, "list_scorecards_by_grade", map[string]any{"grade": "D"})
	if got := len(empty["items"].([]any)); got != 0 {
		t.Fatalf("grade D = %d items, want 0", got)
	}

	first := h.ok(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "limit": 2})
	cursor, _ := first["nextCursor"].(string)
	if len(first["items"].([]any)) != 2 || cursor != "N-0043:7d" {
		t.Fatalf("first page = %v", first)
	}
	second := h.ok(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "limit": 2, "after": cursor})
	if len(second["items"].([]any)) != 2 || second["nextCursor"] != nil {
		t.Fatalf("second page = %v", second)
	}
	if first["items"].([]any)[0].(map[string]any)["scorecardId"] == second["items"].([]any)[0].(map[string]any)["scorecardId"] {
		t.Fatal("the pages overlap")
	}
}

func TestListScorecardsByGradeErrors(t *testing.T) {
	h := newHarness(t, nodeFixture())
	h.failWith(t, "list_scorecards_by_grade", map[string]any{"grade": "E"}, "validation-failed")
	h.failWith(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "window": "90d"}, "validation-failed")
	h.failWith(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "limit": 9999}, "validation-failed")
	// A cursor longer than the 80-character bound the use case accepts.
	h.failWith(t, "list_scorecards_by_grade", map[string]any{"grade": "A", "after": strings.Repeat("x", 81)}, "validation-failed")
}
