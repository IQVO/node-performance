package scorecard

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	s := open(t, Window30d, many("h", KindHandover, MinObservations, at(0))...)
	s.Recompute(at(100))
	if err := s.RecordObservation(obs("extra", KindPromiseKept, at(60))); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	back, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	again := back.Snapshot()
	if !reflect.DeepEqual(snap, again) {
		t.Fatalf("round trip lost data:\n%+v\n%+v", snap, again)
	}
	if back.State() != StateGraded || back.Grade() != GradeA || back.Version() != s.Version() || !back.ComputedAt().Equal(s.ComputedAt()) {
		t.Errorf("restored %s %q v%d %v", back.State(), back.Grade(), back.Version(), back.ComputedAt())
	}
}

func TestSnapshotOrdersObservationsByInstantThenId(t *testing.T) {
	s := open(t, Window7d, obs("b", KindFeedApplied, at(5)), obs("c", KindFeedApplied, at(1)), obs("a", KindFeedApplied, at(5)))
	var ids []string
	for _, o := range s.Snapshot().Observations {
		ids = append(ids, o.SourceEventID)
	}
	if got := ids[0] + ids[1] + ids[2]; got != "cab" {
		t.Fatalf("order = %v, want c a b", ids)
	}
}

func TestRestoreRefusesACorruptSnapshot(t *testing.T) {
	valid := func() Snapshot {
		return Snapshot{
			NodeID: "n", Window: Window7d, State: StateProvisional, Metrics: EmptyMetrics(), Version: 1,
			Observations: []Observation{obs("e", KindFeedApplied, at(1))},
		}
	}
	gradedSnap := func() Snapshot {
		s := valid()
		s.State, s.Grade = StateGraded, GradeB
		s.Metrics.Observations = MinObservations
		return s
	}
	for name, mutate := range map[string]func() Snapshot{
		"node id":                func() Snapshot { s := valid(); s.NodeID = "a:b"; return s },
		"empty node id":          func() Snapshot { s := valid(); s.NodeID = ""; return s },
		"window":                 func() Snapshot { s := valid(); s.Window = "1d"; return s },
		"version zero":           func() Snapshot { s := valid(); s.Version = 0; return s },
		"negative version":       func() Snapshot { s := valid(); s.Version = -3; return s },
		"state":                  func() Snapshot { s := valid(); s.State = "DONE"; return s },
		"metrics rate":           func() Snapshot { s := valid(); s.Metrics.RejectRate = 1.5; return s },
		"provisional with grade": func() Snapshot { s := valid(); s.Grade = GradeA; return s },
		"graded without grade":   func() Snapshot { s := gradedSnap(); s.Grade = ""; return s },
		"graded bad grade":       func() Snapshot { s := gradedSnap(); s.Grade = "E"; return s },
		"graded below minimum": func() Snapshot {
			s := gradedSnap()
			s.Metrics.Observations = MinObservations - 1
			return s
		},
		"observation invalid": func() Snapshot { s := valid(); s.Observations[0].Kind = "NOPE"; return s },
		"observation twice": func() Snapshot {
			s := valid()
			s.Observations = append(s.Observations, obs("e", KindHandover, at(2)))
			return s
		},
	} {
		got, err := Restore(mutate())
		if got != nil || !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: Restore = %v, %v, want ErrInvalidSnapshot", name, got, err)
		}
	}
	if _, err := Restore(gradedSnap()); err != nil {
		t.Errorf("a graded snapshot at the minimum is valid: %v", err)
	}
	if _, err := Restore(valid()); err != nil {
		t.Errorf("the valid snapshot must restore: %v", err)
	}
}

func TestRestoreNormalisesInstantsToUTC(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	snap := Snapshot{
		NodeID: "n", Window: Window7d, State: StateProvisional, Metrics: EmptyMetrics(), Version: 1,
		ComputedAt:   time.Date(2026, 10, 22, 9, 0, 0, 0, brt),
		Observations: []Observation{obs("e", KindFeedApplied, time.Date(2026, 10, 22, 9, 0, 0, 0, brt))},
	}
	s, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if s.ComputedAt().Location() != time.UTC || s.Snapshot().Observations[0].At.Location() != time.UTC {
		t.Fatal("restored instants are UTC")
	}
	if got, _ := Restore(Snapshot{NodeID: "n", Window: Window7d, State: StateProvisional, Metrics: EmptyMetrics(), Version: 1}); !got.ComputedAt().IsZero() {
		t.Fatal("a zero computedAt stays zero")
	}
}
