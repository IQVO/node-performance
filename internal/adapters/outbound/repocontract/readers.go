package repocontract

import (
	"context"
	"reflect"
	"testing"

	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

var readerCases = []named{
	{"ByNode returns the views of the node, 7d first, whatever the save order", rdByNode},
	{"ByNode of a node nothing was observed about is an empty list", rdByNodeEmpty},
	{"a view carries the state, the wire grade, the metrics and computedAt", rdViewFields},
	{"ByGrade filters by the wire grade and orders by scorecardId in byte order", rdByGrade},
	{"ByGrade filters by window", rdByGradeWindow},
	{"ByGrade pages with a keyset cursor", rdByGradePages},
}

func scorecardIDs(items []views.Scorecard) []string {
	out := make([]string, 0, len(items))
	for _, v := range items {
		out = append(out, v.ScorecardID)
	}
	return out
}

func rdByNode(t *testing.T, f Fixture) {
	Seed(t, f, "node-1", scorecard.Window30d, 1)
	Seed(t, f, "node-1", scorecard.Window7d, 1)
	Seed(t, f, "node-2", scorecard.Window7d, 1)
	got, err := f.Reader.ByNode(context.Background(), "node-1")
	noErr(t, err)
	if !reflect.DeepEqual(scorecardIDs(got), []string{"node-1:7d", "node-1:30d"}) {
		t.Fatalf("ByNode = %v", scorecardIDs(got))
	}
}

func rdByNodeEmpty(t *testing.T, f Fixture) {
	got, err := f.Reader.ByNode(context.Background(), "ghost")
	noErr(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("ByNode of an unknown node = %#v, want an empty non-nil list", got)
	}
}

func rdViewFields(t *testing.T, f Fixture) {
	graded := SeedGraded(t, f, "node-1", scorecard.Window30d)
	Seed(t, f, "node-1", scorecard.Window7d, 2)
	got, err := f.Reader.ByNode(context.Background(), "node-1")
	noErr(t, err)
	if len(got) != 2 {
		t.Fatalf("views = %+v", got)
	}
	want7 := views.Scorecard{
		ScorecardID: "node-1:7d", NodeID: "node-1", Window: "7d", State: "PROVISIONAL", Grade: "PROVISIONAL",
		Metrics: views.MetricsFrom(scorecard.EmptyMetrics()),
	}
	if !reflect.DeepEqual(got[0], want7) {
		t.Fatalf("provisional view:\n got %+v\nwant %+v", got[0], want7)
	}
	want30 := views.Scorecard{
		ScorecardID: "node-1:30d", NodeID: "node-1", Window: "30d", State: "GRADED", Grade: "A",
		Metrics: views.MetricsFrom(graded.Metrics()), ComputedAt: At(120),
	}
	if !reflect.DeepEqual(got[1], want30) || got[1].Metrics.Observations != scorecard.MinObservations {
		t.Fatalf("graded view:\n got %+v\nwant %+v", got[1], want30)
	}
}

// seedGradeMix stores five scorecards: node-1:7d, node-2:7d and node-2:30d
// graded A; node-1:30d and N-3:7d PROVISIONAL.
func seedGradeMix(t *testing.T, f Fixture) {
	t.Helper()
	SeedGraded(t, f, "node-1", scorecard.Window7d)
	Seed(t, f, "node-1", scorecard.Window30d, 1)
	SeedGraded(t, f, "node-2", scorecard.Window7d)
	SeedGraded(t, f, "node-2", scorecard.Window30d)
	Seed(t, f, "N-3", scorecard.Window7d, 1)
}

func byGrade(t *testing.T, f Fixture, filter repository.ScorecardFilter) views.ScorecardPage {
	t.Helper()
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	page, err := f.Reader.ByGrade(context.Background(), filter)
	noErr(t, err)
	if page.Items == nil {
		t.Fatal("a page lists its items as an empty slice, never nil")
	}
	return page
}

func rdByGrade(t *testing.T, f Fixture) {
	seedGradeMix(t, f)
	a := byGrade(t, f, repository.ScorecardFilter{Grade: "A"})
	if !reflect.DeepEqual(scorecardIDs(a.Items), []string{"node-1:7d", "node-2:30d", "node-2:7d"}) || a.NextCursor != "" {
		t.Fatalf("grade A = %v cursor %q", scorecardIDs(a.Items), a.NextCursor)
	}
	p := byGrade(t, f, repository.ScorecardFilter{Grade: "PROVISIONAL"})
	if !reflect.DeepEqual(scorecardIDs(p.Items), []string{"N-3:7d", "node-1:30d"}) {
		t.Fatalf("PROVISIONAL = %v (byte order: an upper-case id sorts before a lower-case one)", scorecardIDs(p.Items))
	}
	if d := byGrade(t, f, repository.ScorecardFilter{Grade: "D"}); len(d.Items) != 0 || d.NextCursor != "" {
		t.Fatalf("grade D = %+v", d)
	}
}

func rdByGradeWindow(t *testing.T, f Fixture) {
	seedGradeMix(t, f)
	got := byGrade(t, f, repository.ScorecardFilter{Grade: "A", Window: scorecard.Window30d})
	if !reflect.DeepEqual(scorecardIDs(got.Items), []string{"node-2:30d"}) {
		t.Fatalf("A in 30d = %v", scorecardIDs(got.Items))
	}
	got = byGrade(t, f, repository.ScorecardFilter{Grade: "PROVISIONAL", Window: scorecard.Window7d})
	if !reflect.DeepEqual(scorecardIDs(got.Items), []string{"N-3:7d"}) {
		t.Fatalf("PROVISIONAL in 7d = %v", scorecardIDs(got.Items))
	}
}

func rdByGradePages(t *testing.T, f Fixture) {
	seedGradeMix(t, f)
	first := byGrade(t, f, repository.ScorecardFilter{Grade: "A", Limit: 2})
	if !reflect.DeepEqual(scorecardIDs(first.Items), []string{"node-1:7d", "node-2:30d"}) || first.NextCursor != "node-2:30d" {
		t.Fatalf("page 1 = %v cursor %q", scorecardIDs(first.Items), first.NextCursor)
	}
	second := byGrade(t, f, repository.ScorecardFilter{Grade: "A", Limit: 2, After: first.NextCursor})
	if !reflect.DeepEqual(scorecardIDs(second.Items), []string{"node-2:7d"}) || second.NextCursor != "" {
		t.Fatalf("page 2 = %v cursor %q", scorecardIDs(second.Items), second.NextCursor)
	}
	exact := byGrade(t, f, repository.ScorecardFilter{Grade: "A", Limit: 3})
	if len(exact.Items) != 3 || exact.NextCursor != "" {
		t.Fatalf("a page of exactly the remaining rows has no cursor: %v cursor %q", scorecardIDs(exact.Items), exact.NextCursor)
	}
	past := byGrade(t, f, repository.ScorecardFilter{Grade: "A", After: "node-2:7d"})
	if len(past.Items) != 0 {
		t.Fatalf("after the last id: %v", scorecardIDs(past.Items))
	}
}
