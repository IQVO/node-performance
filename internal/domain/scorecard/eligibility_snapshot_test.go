package scorecard

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestFastTierSnapshotRestoreRoundTrip(t *testing.T) {
	s := suspended7d(t)
	lift(t, s, midday(1))
	s.Recompute(midday(1))
	snap := s.Snapshot()
	want := FastTierState{Suspended: true, Streak: 1, StreakDay: utcDay(midday(1))}
	if snap.FastTier != want {
		t.Fatalf("Snapshot().FastTier = %+v, want %+v", snap.FastTier, want)
	}
	back, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap, back.Snapshot()) || back.FastTier() != want {
		t.Fatalf("round trip lost the fast tier state:\n%+v\n%+v", snap, back.Snapshot())
	}
}

func TestFastTierRestoreNormalisesTheStreakDayToUTCMidnight(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	snap := Snapshot{
		NodeID: "n", Window: Window7d, State: StateProvisional, Metrics: EmptyMetrics(), Version: 1,
		FastTier: FastTierState{Suspended: true, Streak: 1, StreakDay: time.Date(2026, 10, 23, 22, 30, 0, 0, brt)},
	}
	s, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.FastTier().StreakDay, time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("streak day = %v, want %v", got, want)
	}
}

func TestFastTierRestoreRefusesACorruptState(t *testing.T) {
	day := utcDay(base)
	for name, tc := range map[string]struct {
		window Window
		state  FastTierState
	}{
		"suspended 30d":         {Window30d, FastTierState{Suspended: true}},
		"streak at the bar":     {Window7d, FastTierState{Suspended: true, Streak: reinstateAfterDays, StreakDay: day}},
		"negative streak":       {Window7d, FastTierState{Suspended: true, Streak: -1, StreakDay: day}},
		"eligible with streak":  {Window7d, FastTierState{Streak: 1, StreakDay: day}},
		"streak without a day":  {Window7d, FastTierState{Suspended: true, Streak: 1}},
		"a day without streak":  {Window7d, FastTierState{Suspended: true, StreakDay: day}},
		"streak on a 30d card":  {Window30d, FastTierState{Streak: 1, StreakDay: day}},
		"day on an eligible 7d": {Window7d, FastTierState{StreakDay: day}},
	} {
		snap := Snapshot{NodeID: "n", Window: tc.window, State: StateProvisional, Metrics: EmptyMetrics(), Version: 1, FastTier: tc.state}
		if got, err := Restore(snap); got != nil || !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore = %v, %v, want ErrInvalidSnapshot", name, got, err)
		}
	}
}

func TestFastTiersAreSameDayAndNextDay(t *testing.T) {
	if got := FastTiers(); !reflect.DeepEqual(got, []Tier{TierSameDay, TierNextDay}) {
		t.Fatalf("FastTiers = %v", got)
	}
}
