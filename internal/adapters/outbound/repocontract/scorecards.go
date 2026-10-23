package repocontract

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

var scorecardCases = []named{
	{"a miss is ports.ErrNotFound", scGetMiss},
	{"a stored scorecard round-trips with every observation", scRoundTrip},
	{"a graded scorecard round-trips its grade, metrics and computedAt", scRoundTripGraded},
	{"creating an id that exists is ErrConcurrentUpdate", scCreateTwice},
	{"an update expects the stored version to be version-1", scUpdateVersions},
	{"updating an id that was never stored is ErrConcurrentUpdate", scUpdateMissing},
	{"a save replaces the stored observation set", scReplacesObservations},
	{"ListDue returns the due scorecards by id, up to the limit", scListDue},
}

func scGetMiss(t *testing.T, f Fixture) {
	_, err := f.Scorecards.Get(context.Background(), "ghost:7d")
	wantErr(t, err, ports.ErrNotFound)
}

func save(t *testing.T, f Fixture, s *scorecard.NodeScorecard) {
	t.Helper()
	noErr(t, f.Scorecards.Save(context.Background(), s))
}

func load(t *testing.T, f Fixture, id string) *scorecard.NodeScorecard {
	t.Helper()
	s, err := f.Scorecards.Get(context.Background(), id)
	noErr(t, err)
	return s
}

func assertSnapshotEqual(t *testing.T, got, want scorecard.Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot differs:\n got %+v\nwant %+v", got, want)
	}
}

func scRoundTrip(t *testing.T, f Fixture) {
	s := Seed(t, f, "node-1", scorecard.Window7d, 3)
	got := load(t, f, "node-1:7d")
	assertSnapshotEqual(t, got.Snapshot(), s.Snapshot())
	if got.Version() != 3 || got.ObservationCount() != 3 || got.State() != scorecard.StateProvisional || !got.DueForRecompute(Now) {
		t.Fatalf("loaded scorecard: version %d observations %d state %s", got.Version(), got.ObservationCount(), got.State())
	}
}

func scRoundTripGraded(t *testing.T, f Fixture) {
	s := SeedGraded(t, f, "node-1", scorecard.Window30d)
	got := load(t, f, "node-1:30d")
	assertSnapshotEqual(t, got.Snapshot(), s.Snapshot())
	if got.Grade() != scorecard.GradeA || got.State() != scorecard.StateGraded || !got.ComputedAt().Equal(At(120)) {
		t.Fatalf("loaded %s %q computedAt %v", got.State(), got.Grade(), got.ComputedAt())
	}
	if got.Metrics() != s.Metrics() {
		t.Fatalf("metrics %+v, want %+v", got.Metrics(), s.Metrics())
	}
}

func scCreateTwice(t *testing.T, f Fixture) {
	save(t, f, NewScorecard(t, "node-1", scorecard.Window7d))
	err := f.Scorecards.Save(context.Background(), NewScorecard(t, "node-1", scorecard.Window7d))
	wantErr(t, err, ports.ErrConcurrentUpdate)
}

func scUpdateVersions(t *testing.T, f Fixture) {
	Seed(t, f, "node-1", scorecard.Window7d, 1)
	first, second := load(t, f, "node-1:7d"), load(t, f, "node-1:7d")
	noErr(t, first.RecordObservation(Handover(t, "extra-1", 10)))
	save(t, f, first)
	noErr(t, second.RecordObservation(Handover(t, "extra-2", 11)))
	wantErr(t, f.Scorecards.Save(context.Background(), second), ports.ErrConcurrentUpdate)
	if got := load(t, f, "node-1:7d"); got.Version() != 2 || got.ObservationCount() != 2 {
		t.Fatalf("the stale save must change nothing: version %d observations %d", got.Version(), got.ObservationCount())
	}
	// two versions ahead is as stale as one behind
	ahead := load(t, f, "node-1:7d")
	noErr(t, ahead.RecordObservation(Handover(t, "extra-3", 12)))
	noErr(t, ahead.RecordObservation(Handover(t, "extra-4", 13)))
	wantErr(t, f.Scorecards.Save(context.Background(), ahead), ports.ErrConcurrentUpdate)
}

func scUpdateMissing(t *testing.T, f Fixture) {
	s := NewScorecard(t, "node-1", scorecard.Window7d)
	noErr(t, s.RecordObservation(Handover(t, "extra-1", 10))) // version 2, nothing stored
	wantErr(t, f.Scorecards.Save(context.Background(), s), ports.ErrConcurrentUpdate)
}

func scReplacesObservations(t *testing.T, f Fixture) {
	Seed(t, f, "node-1", scorecard.Window7d, 4)
	loaded := load(t, f, "node-1:7d")
	// every observation is older than the 7-day window at this instant
	Recomputed(t, f, loaded, At(10*24*60))
	got := load(t, f, "node-1:7d")
	if got.ObservationCount() != 0 || got.Version() != 5 {
		t.Fatalf("after the window slid: observations %d version %d", got.ObservationCount(), got.Version())
	}
	noErr(t, got.RecordObservation(Handover(t, "node-1-7d-0", 0))) // an id dropped before may be counted again
	save(t, f, got)
	if load(t, f, "node-1:7d").ObservationCount() != 1 {
		t.Fatal("a dropped source id is free again")
	}
}

func scListDue(t *testing.T, f Fixture) {
	ctx := context.Background()
	save(t, f, NewScorecard(t, "b-node", scorecard.Window7d))
	save(t, f, NewScorecard(t, "a-node", scorecard.Window7d))
	Recomputed(t, f, Seed(t, f, "c-node", scorecard.Window30d, 1), At(5))
	expiring := Seed(t, f, "d-node", scorecard.Window7d, 1)
	Recomputed(t, f, expiring, At(5))
	got, err := f.Scorecards.ListDue(ctx, At(5), 10)
	noErr(t, err)
	if !reflect.DeepEqual(got, []string{"a-node:7d", "b-node:7d"}) {
		t.Fatalf("due at now = %v, want the two dirty scorecards by id", got)
	}
	exp, ok := expiring.ExpiresAt()
	if !ok {
		t.Fatal("expiring has an observation")
	}
	got, err = f.Scorecards.ListDue(ctx, exp.Add(-time.Microsecond), 10)
	noErr(t, err)
	if len(got) != 2 {
		t.Fatalf("one microsecond before the oldest observation leaves, due = %v", got)
	}
	got, err = f.Scorecards.ListDue(ctx, exp, 10)
	noErr(t, err)
	if !reflect.DeepEqual(got, []string{"a-node:7d", "b-node:7d", "d-node:7d"}) {
		t.Fatalf("at the expiry instant due = %v", got)
	}
	got, err = f.Scorecards.ListDue(ctx, exp, 2)
	noErr(t, err)
	if !reflect.DeepEqual(got, []string{"a-node:7d", "b-node:7d"}) {
		t.Fatalf("limit 2: due = %v", got)
	}
	got, err = f.Scorecards.ListDue(ctx, At(-1000), 10)
	noErr(t, err)
	if len(got) != 2 {
		t.Fatalf("a dirty scorecard is due whatever the instant: %v", got)
	}
}
