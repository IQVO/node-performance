package scorecard

import (
	"fmt"
	"testing"
	"time"
)

// base is the fixed clock of every test: all instants are minutes from it.
var base = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func at(minutes int) time.Time { return base.Add(time.Duration(minutes) * time.Minute) }

func mustNode(t *testing.T) NodeID {
	t.Helper()
	n, err := NewNodeID("node-0f3a91")
	if err != nil {
		t.Fatalf("NewNodeID: %v", err)
	}
	return n
}

// obs builds an observation directly (bypassing the constructors) so a test
// controls every field.
func obs(id string, kind Kind, instant time.Time) Observation {
	return Observation{SourceEventID: id, Kind: kind, At: instant}
}

// many returns n observations of one kind, one minute apart from start.
func many(prefix string, kind Kind, n int, start time.Time) []Observation {
	out := make([]Observation, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Observation{SourceEventID: fmt.Sprintf("%s-%d", prefix, i), Kind: kind, At: start.Add(time.Duration(i) * time.Minute), OnTime: true})
	}
	return out
}

// open returns an opened scorecard of the window with every observation of
// list recorded.
func open(t *testing.T, w Window, list ...Observation) *NodeScorecard {
	t.Helper()
	if len(list) == 0 {
		t.Fatal("open needs at least one observation")
	}
	s, err := Open(mustNode(t), w, list[0])
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, o := range list[1:] {
		if err := s.RecordObservation(o); err != nil {
			t.Fatalf("RecordObservation(%s): %v", o.SourceEventID, err)
		}
	}
	return s
}
