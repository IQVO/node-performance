package repocontract

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// Now is the fixed clock of the contract: whole seconds, so every adapter
// (Postgres keeps microseconds) round-trips it exactly.
var Now = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// At returns Now plus the given minutes.
func At(minutes int) time.Time { return Now.Add(time.Duration(minutes) * time.Minute) }

// Handover builds an on-time handover observation of the id.
func Handover(t testing.TB, id string, minutes int) scorecard.Observation {
	t.Helper()
	o, err := scorecard.NewHandover(id, At(minutes), At(minutes+60))
	if err != nil {
		t.Fatalf("NewHandover: %v", err)
	}
	return o
}

// Feed builds a feed observation of the id.
func Feed(t testing.TB, id string, minutes int) scorecard.Observation {
	t.Helper()
	o, err := scorecard.NewFeedApplied(id, At(minutes))
	if err != nil {
		t.Fatalf("NewFeedApplied: %v", err)
	}
	return o
}

// NewScorecard opens the scorecard of (node, window) from one on-time handover
// observation (node-window-0): version 1, not stored.
func NewScorecard(t testing.TB, node string, w scorecard.Window) *scorecard.NodeScorecard {
	t.Helper()
	id, err := scorecard.NewNodeID(node)
	if err != nil {
		t.Fatalf("NewNodeID: %v", err)
	}
	s, err := scorecard.Open(id, w, Handover(t, fmt.Sprintf("%s-%s-0", node, w), 0))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s
}

// Seed stores the scorecard of (node, window) holding n on-time handovers
// (node-window-0 ... node-window-(n-1), one minute apart from Now), one save
// per observation, the way the service writes it, and returns the aggregate as
// stored (version n).
func Seed(t testing.TB, f Fixture, node string, w scorecard.Window, n int) *scorecard.NodeScorecard {
	t.Helper()
	ctx := context.Background()
	s := NewScorecard(t, node, w)
	noErr(t, f.Scorecards.Save(ctx, s))
	for i := 1; i < n; i++ {
		noErr(t, s.RecordObservation(Handover(t, fmt.Sprintf("%s-%s-%d", node, w, i), i)))
		noErr(t, f.Scorecards.Save(ctx, s))
	}
	return s
}

// Recomputed recomputes a stored scorecard at the instant and saves it.
func Recomputed(t testing.TB, f Fixture, s *scorecard.NodeScorecard, now time.Time) {
	t.Helper()
	s.Recompute(now)
	noErr(t, f.Scorecards.Save(context.Background(), s))
}

// SeedGraded seeds MinObservations handovers and recomputes at Now+120
// minutes: GRADED, grade A.
func SeedGraded(t testing.TB, f Fixture, node string, w scorecard.Window) *scorecard.NodeScorecard {
	t.Helper()
	s := Seed(t, f, node, w, scorecard.MinObservations)
	Recomputed(t, f, s, At(120))
	return s
}

// RaisedEvents returns the events of a scorecard that is graded and then
// worsens past a threshold: NodeScorecardUpdated, NodeScorecardUpdated,
// NodeThresholdBreached, in the order Recompute raised them.
func RaisedEvents(t testing.TB) []shared.Event {
	t.Helper()
	s := NewScorecard(t, "node-9", scorecard.Window7d)
	for i := 1; i < scorecard.MinObservations; i++ {
		noErr(t, s.RecordObservation(Handover(t, fmt.Sprintf("ok-%d", i), i)))
	}
	events := s.Recompute(At(120))
	for i := 0; i < 6; i++ {
		late, err := scorecard.NewHandover(fmt.Sprintf("late-%d", i), At(200+i), At(100))
		if err != nil {
			t.Fatalf("NewHandover: %v", err)
		}
		noErr(t, s.RecordObservation(late))
	}
	events = append(events, s.Recompute(At(240))...)
	if len(events) != 3 {
		t.Fatalf("the fixture must raise 3 events, got %d", len(events))
	}
	return events
}
