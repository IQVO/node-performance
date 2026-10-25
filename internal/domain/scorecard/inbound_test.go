package scorecard

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func statuses(received, other int) []string {
	out := make([]string, 0, received+other)
	for i := 0; i < received; i++ {
		out = append(out, "RECEIVED")
	}
	for i := 0; i < other; i++ {
		out = append(out, "DAMAGED")
	}
	return out
}

func TestNewInboundReceiptCountsReceivedCartons(t *testing.T) {
	node := mustNode(t)
	r, err := NewInboundReceipt("shp-1", node, at(10), []string{"RECEIVED", "MISSING", "UNEXPECTED", "DAMAGED", "RECEIVED", "WHATEVER"})
	if err != nil {
		t.Fatal(err)
	}
	if r.CartonsTotal != 6 || r.CartonsReceived != 2 {
		t.Fatalf("got total %d received %d", r.CartonsTotal, r.CartonsReceived)
	}
	empty, err := NewInboundReceipt("shp-2", node, at(10), nil)
	if err != nil || empty.CartonsTotal != 0 || empty.CartonsReceived != 0 {
		t.Fatalf("empty receipt: %+v %v", empty, err)
	}
}

func TestNewInboundReceiptValidates(t *testing.T) {
	node := mustNode(t)
	cases := map[string]func() error{
		"empty shipment id": func() error { _, e := NewInboundReceipt("", node, at(1), nil); return e },
		"long shipment id": func() error {
			id := make([]byte, MaxSourceEventID+1)
			for i := range id {
				id[i] = 'a'
			}
			_, e := NewInboundReceipt(string(id), node, at(1), nil)
			return e
		},
		"bad node":         func() error { _, e := NewInboundReceipt("s", "bad:node", at(1), nil); return e },
		"zero receivedAt":  func() error { _, e := NewInboundReceipt("s", node, time.Time{}, nil); return e },
		"shipment no id":   func() error { _, e := NewInboundShipment("", node, at(1)); return e },
		"shipment bad nod": func() error { _, e := NewInboundShipment("s", "", at(1)); return e },
		"shipment no time": func() error { _, e := NewInboundShipment("s", node, time.Time{}); return e },
	}
	for name, f := range cases {
		if err := f(); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: want validation error, got %v", name, err)
		}
	}
	max := make([]byte, MaxSourceEventID)
	for i := range max {
		max[i] = 'a'
	}
	if _, err := NewInboundReceipt(string(max), node, at(1), nil); err != nil {
		t.Errorf("a shipment id of exactly the limit must pass: %v", err)
	}
	s, err := NewInboundShipment("s", node, base.Add(1500*time.Nanosecond))
	if err != nil || s.ShippedAt.Nanosecond() != 1000 {
		t.Errorf("shippedAt must be truncated to microseconds: %v %v", s.ShippedAt, err)
	}
}

func TestJoinInboundMinutes(t *testing.T) {
	node := mustNode(t)
	r, _ := NewInboundReceipt("s", node, at(100), statuses(1, 1))
	cases := []struct {
		name    string
		shipped time.Time
		want    int
	}{
		{"exact minutes", at(40), 60},
		{"truncates the partial minute", at(40).Add(-59 * time.Second), 60},
		{"one minute less a second is zero", at(100).Add(-59 * time.Second), 0},
		{"same instant", at(100), 0},
		{"received before shipped is clamped to zero", at(130), 0},
		{"one nanosecond before is clamped to zero", at(100).Add(time.Nanosecond), 0},
	}
	for _, c := range cases {
		o := JoinInbound(r, c.shipped)
		if o.Minutes != c.want {
			t.Errorf("%s: got %d want %d", c.name, o.Minutes, c.want)
		}
		if o.CartonsTotal != 2 || o.CartonsReceived != 1 || !o.ReceivedAt.Equal(r.ReceivedAt) {
			t.Errorf("%s: carton counts or instant lost: %+v", c.name, o)
		}
	}
}

func obsMin(receivedAt time.Time, minutes, total, received int) InboundObservation {
	return InboundObservation{ReceivedAt: receivedAt, Minutes: minutes, CartonsTotal: total, CartonsReceived: received}
}

func TestComputeInboundNoData(t *testing.T) {
	now := at(0)
	if m := ComputeInbound(nil, now, Window7d); m.TimelinessP95Minutes != nil || m.AccuracyRate != nil {
		t.Fatalf("no observation must give no metric: %+v", m)
	}
	old := obsMin(now.Add(-7*24*time.Hour), 5, 1, 1)
	if m := ComputeInbound([]InboundObservation{old}, now, Window7d); m.TimelinessP95Minutes != nil || m.AccuracyRate != nil {
		t.Fatalf("an observation exactly at now-window is outside: %+v", m)
	}
}

func TestComputeInboundWindowEdge(t *testing.T) {
	now := at(0)
	edge := now.Add(-7 * 24 * time.Hour)
	in := obsMin(edge.Add(time.Nanosecond), 9, 2, 1)
	m := ComputeInbound([]InboundObservation{in}, now, Window7d)
	if m.TimelinessP95Minutes == nil || *m.TimelinessP95Minutes != 9 {
		t.Fatalf("one nanosecond inside the window must count: %+v", m)
	}
	m30 := ComputeInbound([]InboundObservation{obsMin(edge, 3, 1, 1)}, now, Window30d)
	if m30.TimelinessP95Minutes == nil || *m30.TimelinessP95Minutes != 3 {
		t.Fatalf("the 30d window still holds a 7d-old receipt: %+v", m30)
	}
	future := obsMin(now.Add(time.Hour), 4, 1, 1)
	if m := ComputeInbound([]InboundObservation{future}, now, Window7d); m.TimelinessP95Minutes == nil {
		t.Fatal("a future instant counts (clocks skew), like the other observations")
	}
}

func TestComputeInboundSingleSample(t *testing.T) {
	m := ComputeInbound([]InboundObservation{obsMin(at(-5), 42, 4, 3)}, at(0), Window7d)
	if *m.TimelinessP95Minutes != 42 {
		t.Fatalf("one sample is its own p95: %d", *m.TimelinessP95Minutes)
	}
	if *m.AccuracyRate != 0.75 {
		t.Fatalf("accuracy: %v", *m.AccuracyRate)
	}
}

func TestComputeInboundPercentileEdges(t *testing.T) {
	// nearest rank: ceil(0.95*n)-th smallest.
	cases := []struct{ n, wantRank int }{{1, 1}, {2, 2}, {19, 19}, {20, 19}, {21, 20}, {40, 38}, {100, 95}}
	for _, c := range cases {
		t.Run(fmt.Sprint(c.n), func(t *testing.T) {
			var list []InboundObservation
			// minutes 1..n, supplied in reverse so sorting matters.
			for i := c.n; i >= 1; i-- {
				list = append(list, obsMin(at(-1), i, 1, 1))
			}
			got := *ComputeInbound(list, at(0), Window7d).TimelinessP95Minutes
			if got != c.wantRank {
				t.Fatalf("n=%d: got %d want %d", c.n, got, c.wantRank)
			}
		})
	}
}

func TestComputeInboundAccuracyIsPooledAndZeroCartonReceiptsAddNoWeight(t *testing.T) {
	list := []InboundObservation{
		obsMin(at(-1), 10, 10, 10), // perfect receipt of ten cartons
		obsMin(at(-2), 20, 2, 0),   // bad receipt of two cartons
		obsMin(at(-3), 30, 0, 0),   // no cartons: timeliness only
	}
	m := ComputeInbound(list, at(0), Window7d)
	if got := *m.AccuracyRate; got != 10.0/12.0 {
		t.Fatalf("pooled accuracy 10/12, got %v", got)
	}
	if *m.TimelinessP95Minutes != 30 {
		t.Fatalf("three samples: p95 is the third, got %d", *m.TimelinessP95Minutes)
	}
	only := ComputeInbound([]InboundObservation{obsMin(at(-1), 7, 0, 0)}, at(0), Window7d)
	if only.AccuracyRate != nil || *only.TimelinessP95Minutes != 7 {
		t.Fatalf("a receipt with no cartons gives timeliness but no accuracy: %+v", only)
	}
}

func TestComputeInboundDoesNotMutateInput(t *testing.T) {
	list := []InboundObservation{obsMin(at(-1), 3, 1, 1), obsMin(at(-1), 1, 1, 1), obsMin(at(-1), 2, 1, 1)}
	ComputeInbound(list, at(0), Window7d)
	if list[0].Minutes != 3 || list[1].Minutes != 1 || list[2].Minutes != 2 {
		t.Fatalf("input reordered: %+v", list)
	}
}
