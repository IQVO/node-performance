package repocontract

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// tierDay is the UTC day of a streak (a DATE column keeps no time of day).
var tierDay = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)

// suspendedSnapshot is the snapshot of a stored 7d scorecard of the node at
// version 1: GRADED with 50 of 60 handovers on time, suspended from the fast
// tiers with no streak.
func suspendedSnapshot(t testing.TB, node string) scorecard.Snapshot {
	t.Helper()
	s := NewScorecard(t, node, scorecard.Window7d)
	for i := 1; i < 60; i++ {
		o := Handover(t, fmt.Sprintf("%s-h-%d", node, i), i)
		if i >= 50 {
			late, err := scorecard.NewHandover(o.SourceEventID, o.At, At(-1))
			noErr(t, err)
			o = late
		}
		noErr(t, s.RecordObservation(o))
	}
	s.Recompute(At(120))
	snap := s.Snapshot()
	snap.Version = 1
	return snap
}

// restoreAndSave restores the snapshot and stores it as a new scorecard.
func restoreAndSave(t *testing.T, f Fixture, snap scorecard.Snapshot) *scorecard.NodeScorecard {
	t.Helper()
	s, err := scorecard.Restore(snap)
	noErr(t, err)
	save(t, f, s)
	return s
}

func scFastTierRoundTrip(t *testing.T, f Fixture) {
	states := []scorecard.FastTierState{
		{Suspended: true},
		{Suspended: true, Streak: 2, StreakDay: tierDay},
	}
	for i, state := range states {
		node := fmt.Sprintf("tier-%d", i)
		snap := suspendedSnapshot(t, node)
		snap.FastTier = state
		want := restoreAndSave(t, f, snap)
		got := load(t, f, node+":7d")
		assertSnapshotEqual(t, got.Snapshot(), want.Snapshot())
		if got.FastTier() != state || got.FastTier().Eligible() {
			t.Fatalf("fast tier = %+v, want %+v", got.FastTier(), state)
		}
	}
	other := Seed(t, f, "tier-eligible", scorecard.Window7d, 2)
	if got := load(t, f, "tier-eligible:7d"); got.FastTier() != (scorecard.FastTierState{}) || other.FastTier() != got.FastTier() {
		t.Fatalf("a new scorecard is eligible with no streak: %+v", got.FastTier())
	}
}

func scFastTierUpdates(t *testing.T, f Fixture) {
	snap := suspendedSnapshot(t, "tier-up")
	snap.FastTier = scorecard.FastTierState{Suspended: true, Streak: 2, StreakDay: tierDay}
	restoreAndSave(t, f, snap)
	s := load(t, f, "tier-up:7d")
	noErr(t, s.RecordObservation(Handover(t, "tier-up-extra", 130)))
	save(t, f, s)
	if got := load(t, f, "tier-up:7d").FastTier(); got != snap.FastTier {
		t.Fatalf("an update that leaves the policy alone keeps it: %+v", got)
	}
	// 51 of 61 on time is under the reinstatement bar: the streak and its day are cleared
	Recomputed(t, f, s, At(2*24*60))
	if got := load(t, f, "tier-up:7d").FastTier(); got != (scorecard.FastTierState{Suspended: true}) {
		t.Fatalf("a recompute under the bar clears the streak: %+v", got)
	}
}

func scFastTierCount(t *testing.T, f Fixture) {
	count := func() int {
		n, err := f.Scorecards.CountFastTierSuspended(context.Background())
		noErr(t, err)
		return n
	}
	if count() != 0 {
		t.Fatal("an empty store suspends nobody")
	}
	Seed(t, f, "tier-eligible", scorecard.Window7d, 2)
	Seed(t, f, "tier-eligible", scorecard.Window30d, 2)
	restoreAndSave(t, f, suspendedSnapshot(t, "tier-a"))
	restoreAndSave(t, f, suspendedSnapshot(t, "tier-b"))
	if got := count(); got != 2 {
		t.Fatalf("suspended nodes = %d, want 2", got)
	}
}
