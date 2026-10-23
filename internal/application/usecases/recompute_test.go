package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// feedN applies n feed events for the node, one minute apart.
func (e env) feedN(t *testing.T, node string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		_, err := e.intake.InventoryFeedApplied(context.Background(), usecases.InventoryFeedApplied{
			Ref: ref(fmt.Sprintf("%s-feed-%d", node, i), i), NodeID: node, AppliedAt: at(i),
		})
		if err != nil {
			t.Fatalf("feed %d: %v", i, err)
		}
	}
}

func eventNames(events []shared.Event) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.SubjectID()+" "+ev.EventName())
	}
	return out
}

func TestRecomputeWithNothingDueDoesNothing(t *testing.T) {
	e := newEnv()
	res, err := e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	wantCounts(t, e, memory.Counts{})
}

func TestRecomputeBelowTheMinimumSavesWithoutPublishing(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 3)
	e.clock.Advance(time.Hour)
	res, err := e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Recomputed: 2}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	s := e.scorecard(t, "node-1:30d")
	if s.State() != scorecard.StateProvisional || !s.ComputedAt().Equal(base.Add(time.Hour)) || s.Metrics().Observations != 3 || s.DueForRecompute(e.clock.Now()) {
		t.Fatalf("state %s computedAt %v metrics %+v", s.State(), s.ComputedAt(), s.Metrics())
	}
	if len(e.store.Outbox().Events()) != 0 {
		t.Fatal("PROVISIONAL to PROVISIONAL publishes nothing")
	}
}

func TestRecomputeGradesAndPublishesOnceThenIsIdle(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", scorecard.MinObservations)
	e.clock.Advance(2 * time.Hour)
	res, err := e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Recomputed: 2, Events: 2}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	got := eventNames(e.store.Outbox().Events())
	want := []string{"node-1:30d NodeScorecardUpdated", "node-1:7d NodeScorecardUpdated"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if s := e.scorecard(t, "node-1:7d"); s.State() != scorecard.StateGraded || s.Grade() != scorecard.GradeA {
		t.Fatalf("state %s grade %q", s.State(), s.Grade())
	}
	res, err = e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{}) {
		t.Fatalf("a second Run has nothing due: %+v, %v", res, err)
	}
}

func TestRecomputeFollowsTheWindowSlide(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", scorecard.MinObservations)
	e.clock.Advance(2 * time.Hour)
	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(8 * 24 * time.Hour) // every observation leaves the 7d window, none leaves the 30d one
	res, err := e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Recomputed: 1, Events: 1}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	s := e.scorecard(t, "node-1:7d")
	if s.State() != scorecard.StateProvisional || s.ObservationCount() != 0 || s.GradeLabel() != "PROVISIONAL" {
		t.Fatalf("7d after the slide: %s %d", s.State(), s.ObservationCount())
	}
	if e.scorecard(t, "node-1:30d").State() != scorecard.StateGraded {
		t.Fatal("the 30d window still holds its observations")
	}
}

func TestRecomputeTakesEveryDueScorecardInBatches(t *testing.T) {
	e := newEnv()
	nodes := usecases.RecomputeBatch + 5
	for i := 0; i < nodes; i++ {
		e.feedN(t, fmt.Sprintf("node-%03d", i), 1)
	}
	res, err := e.recompute.Run(context.Background())
	if err != nil || res.Recomputed != 2*nodes {
		t.Fatalf("Run = %+v, %v; want %d recomputed", res, err, 2*nodes)
	}
}

func TestRecomputeSkipsAScorecardThatKeepsLosingTheRace(t *testing.T) {
	var failing *failingScorecards
	e := newEnv()
	e.feedN(t, "node-1", 1)
	failing = &failingScorecards{ScorecardRepository: e.deps.Scorecards, err: ports.ErrConcurrentUpdate, n: 1000}
	deps := e.deps
	deps.Scorecards = failing
	res, err := usecases.NewRecompute(deps).Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Conflicts: 2}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if !e.scorecard(t, "node-1:7d").DueForRecompute(base) {
		t.Fatal("a skipped scorecard stays due")
	}
	if len(e.store.Outbox().Events()) != 0 {
		t.Fatal("a failed unit appends nothing")
	}
}

func TestRecomputeRetriesALostRace(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", scorecard.MinObservations)
	deps := e.deps
	deps.Scorecards = &failingScorecards{ScorecardRepository: e.deps.Scorecards, err: ports.ErrConcurrentUpdate, n: 1}
	res, err := usecases.NewRecompute(deps).Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Recomputed: 2, Events: 2}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
}

func TestRecomputeStopsOnAFailureAndSaysWhichScorecard(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	deps := e.deps
	deps.Scorecards = &failingScorecards{ScorecardRepository: e.deps.Scorecards, err: errBoom, n: 1}
	res, err := usecases.NewRecompute(deps).Run(context.Background())
	if !errors.Is(err, errBoom) || res.Recomputed != 0 {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if want := "recompute node-1:30d: "; err.Error()[:len(want)] != want {
		t.Fatalf("the error must name the scorecard: %v", err)
	}
}

func TestRecomputeReportsPortFailures(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	t.Run("list", func(t *testing.T) {
		deps := e.deps
		deps.Scorecards = failingList{e.deps.Scorecards, errBoom}
		if _, err := usecases.NewRecompute(deps).Run(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("outbox", func(t *testing.T) {
		e := newEnv()
		e.feedN(t, "node-1", scorecard.MinObservations)
		deps := e.deps
		deps.Outbox = failingOutbox{errBoom}
		res, err := usecases.NewRecompute(deps).Run(context.Background())
		if !errors.Is(err, errBoom) || res.Events != 0 {
			t.Fatalf("Run = %+v, %v", res, err)
		}
		if e.scorecard(t, "node-1:7d").State() != scorecard.StateProvisional {
			t.Fatal("the save must roll back with the failed outbox append")
		}
	})
	t.Run("load", func(t *testing.T) {
		deps := e.deps
		deps.Scorecards = failingGet{e.deps.Scorecards, errBoom}
		if _, err := usecases.NewRecompute(deps).Run(context.Background()); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRecomputeSkipsWhatVanishedOrIsNoLongerDue(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	deps := e.deps
	deps.Scorecards = fixedDue{e.deps.Scorecards, []string{"ghost:7d", "node-1:7d"}}
	res, err := usecases.NewRecompute(deps).Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
}

func TestRecomputeStopsWhenTheContextIsDone(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.recompute.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
