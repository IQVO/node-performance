package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/memory"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

func TestPurchaseOrderIssuedStoresTheFactsAndCountsNothing(t *testing.T) {
	e := newEnv()
	e.issue(t, "po-1", "node-1")
	facts, err := e.store.Facts().Get(context.Background(), "po-1")
	if err != nil {
		t.Fatal(err)
	}
	want := scorecard.PurchaseOrderFacts{PoNumber: "po-1", NodeID: "node-1", IssuedAt: at(0), AckBy: at(600), RequiredShipBy: at(1800)}
	if facts != want {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	wantCounts(t, e, memory.Counts{Facts: 1, Processed: 1})
}

func TestARedeliveredEventChangesNothing(t *testing.T) {
	e := newEnv()
	e.issue(t, "po-1", "node-1")
	before := e.store.Counts()
	out, err := e.intake.PurchaseOrderIssued(context.Background(), usecases.PurchaseOrderIssued{
		Ref: ref("issue-po-1", 0), PoNumber: "po-1", NodeID: "node-1", AckBy: at(1), RequiredShipBy: at(2),
	})
	wantOutcome(t, out, err, usecases.OutcomeDuplicate)
	if e.store.Counts() != before {
		t.Fatal("a duplicate id must change nothing")
	}
	facts, _ := e.store.Facts().Get(context.Background(), "po-1")
	if !facts.AckBy.Equal(at(600)) {
		t.Fatalf("the redelivered event overwrote the facts: %+v", facts)
	}
}

func TestAnAcknowledgementIsJudgedAgainstTheAckWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rejected bool
		minutes  int
		wantKind scorecard.Kind
		wantLate bool
	}{
		{"early acceptance", false, 30, scorecard.KindAckAccepted, false},
		{"acceptance one minute before the late part", false, 539, scorecard.KindAckAccepted, false},
		{"acceptance at the start of the late part", false, 540, scorecard.KindAckAccepted, true},
		{"late rejection", true, 590, scorecard.KindAckRejected, true},
		{"early rejection", true, 10, scorecard.KindAckRejected, false},
	} {
		e := newEnv()
		e.issue(t, "po-1", "node-1")
		out, err := e.intake.PurchaseOrderAcknowledged(context.Background(), usecases.PurchaseOrderAcknowledged{
			Ref: ref("ack-1", tc.minutes), PoNumber: "po-1", NodeID: "node-1", At: at(tc.minutes), Rejected: tc.rejected,
		})
		wantOutcome(t, out, err, usecases.OutcomeApplied)
		for _, w := range []string{"7d", "30d"} {
			o := observationOf(t, e.scorecard(t, "node-1:"+w), "ack-1")
			if o.Kind != tc.wantKind || o.Late != tc.wantLate || !o.At.Equal(at(tc.minutes)) {
				t.Errorf("%s (%s): observation = %+v", tc.name, w, o)
			}
		}
	}
}

func TestTheFirstObservationCreatesBothScorecardsAndTheNextOnesCountOnThem(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	out, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("k-1", 5), NodeID: "node-1", DeliveredAt: at(5)})
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	for _, id := range []string{"node-1:7d", "node-1:30d"} {
		s := e.scorecard(t, id)
		if s.Version() != 1 || s.State() != scorecard.StateProvisional || s.ObservationCount() != 1 || !s.DueForRecompute(base) {
			t.Fatalf("%s: version %d state %s observations %d", id, s.Version(), s.State(), s.ObservationCount())
		}
	}
	out, err = e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("k-2", 6), NodeID: "node-1", DeliveredAt: at(6)})
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	if s := e.scorecard(t, "node-1:30d"); s.Version() != 2 || s.ObservationCount() != 2 {
		t.Fatalf("version %d observations %d", s.Version(), s.ObservationCount())
	}
	e.noScorecard(t, "node-2:7d")
	wantCounts(t, e, memory.Counts{Scorecards: 2, Processed: 2, VersionSum: 4})
}

func TestAnAcknowledgementOfAnUnknownPurchaseOrderIsTransientAndStoresNothing(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	in := usecases.PurchaseOrderAcknowledged{Ref: ref("ack-1", 30), PoNumber: "po-ghost", NodeID: "node-1", At: at(30)}
	if _, err := e.intake.PurchaseOrderAcknowledged(ctx, in); !errors.Is(err, usecases.ErrPurchaseOrderUnknown) {
		t.Fatalf("err = %v, want ErrPurchaseOrderUnknown", err)
	}
	wantCounts(t, e, memory.Counts{})
	// facts that only know requiredShipBy cannot judge an acknowledgement either
	if _, err := e.intake.ShipmentConfirmed(ctx, usecases.ShipmentConfirmed{Ref: ref("ship-1", 5), PoNumber: "po-ghost", NodeID: "node-1", RequiredShipBy: at(1800)}); err != nil {
		t.Fatal(err)
	}
	in.Ref = ref("ack-2", 31)
	if _, err := e.intake.PurchaseOrderAcknowledged(ctx, in); !errors.Is(err, usecases.ErrPurchaseOrderUnknown) {
		t.Fatalf("err = %v, want ErrPurchaseOrderUnknown", err)
	}
	wantCounts(t, e, memory.Counts{Facts: 1, Processed: 1})
}

func TestAnExpiryIsAlwaysLateAndNeedsNoFacts(t *testing.T) {
	e := newEnv()
	out, err := e.intake.PurchaseOrderExpired(context.Background(), usecases.PurchaseOrderExpired{
		Ref: ref("exp-1", 600), PoNumber: "po-1", NodeID: "node-1", AckBy: at(600),
	})
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	o := observationOf(t, e.scorecard(t, "node-1:7d"), "exp-1")
	if o.Kind != scorecard.KindAckExpired || !o.Late || !o.At.Equal(at(600)) {
		t.Fatalf("observation = %+v", o)
	}
}

func TestAHandoverIsJudgedAgainstRequiredShipBy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		minutes int
		onTime  bool
	}{{"before", 1700, true}, {"on the boundary", 1800, true}, {"after", 1801, false}} {
		e := newEnv()
		e.issue(t, "po-1", "node-1")
		out, err := e.intake.PackageHandedOver(context.Background(), usecases.PackageHandedOver{
			Ref: ref("ho-1", tc.minutes), PoNumber: "po-1", NodeID: "node-1", HandedOverAt: at(tc.minutes),
		})
		wantOutcome(t, out, err, usecases.OutcomeApplied)
		if o := observationOf(t, e.scorecard(t, "node-1:30d"), "ho-1"); o.Kind != scorecard.KindHandover || o.OnTime != tc.onTime {
			t.Errorf("%s: observation = %+v", tc.name, o)
		}
	}
}

func TestAHandoverNeedsTheRequiredShipByOfItsPurchaseOrder(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	in := usecases.PackageHandedOver{Ref: ref("ho-1", 5), PoNumber: "po-1", NodeID: "node-1", HandedOverAt: at(5)}
	if _, err := e.intake.PackageHandedOver(ctx, in); !errors.Is(err, usecases.ErrPurchaseOrderUnknown) {
		t.Fatalf("no facts: %v", err)
	}
	// a shipment confirmation alone is enough to judge the handover
	if _, err := e.intake.ShipmentConfirmed(ctx, usecases.ShipmentConfirmed{Ref: ref("ship-1", 5), PoNumber: "po-1", NodeID: "node-1", RequiredShipBy: at(100)}); err != nil {
		t.Fatal(err)
	}
	out, err := e.intake.PackageHandedOver(ctx, in)
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	if o := observationOf(t, e.scorecard(t, "node-1:7d"), "ho-1"); !o.OnTime {
		t.Fatalf("observation = %+v", o)
	}
}

func TestPromisesCountOnlyNodeAttributableBrokenCauses(t *testing.T) {
	ctx := context.Background()
	for cause, counted := range map[string]bool{
		"LATE": true, "LATE_HANDOVER": true, "VENDOR_REJECT": true, "ACK_EXPIRED": true,
		"LOST": false, "RETURNED": false, "SOMETHING_NEW": false, "": false,
	} {
		e := newEnv()
		out, err := e.intake.PromiseBroken(ctx, usecases.PromiseBroken{Ref: ref("b-1", 50), NodeID: "node-1", At: at(40), Cause: cause})
		if counted {
			wantOutcome(t, out, err, usecases.OutcomeApplied)
			if o := observationOf(t, e.scorecard(t, "node-1:7d"), "b-1"); o.Kind != scorecard.KindPromiseBroken || !o.At.Equal(at(40)) {
				t.Errorf("%s: observation = %+v", cause, o)
			}
			continue
		}
		wantOutcome(t, out, err, usecases.OutcomeIgnored)
		e.noScorecard(t, "node-1:7d")
		wantCounts(t, e, memory.Counts{Processed: 1})
	}
}

func TestABrokenPromiseWithoutDeliveredAtUsesTheEventTime(t *testing.T) {
	e := newEnv()
	out, err := e.intake.PromiseBroken(context.Background(), usecases.PromiseBroken{Ref: ref("b-1", 50), NodeID: "node-1", Cause: "LATE"})
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	if o := observationOf(t, e.scorecard(t, "node-1:7d"), "b-1"); !o.At.Equal(at(50)) {
		t.Fatalf("At = %v, want the envelope time", o.At)
	}
}

func TestAFeedIsAnObservationAtItsAppliedInstant(t *testing.T) {
	e := newEnv()
	out, err := e.intake.InventoryFeedApplied(context.Background(), usecases.InventoryFeedApplied{Ref: ref("f-1", 9), NodeID: "node-1", AppliedAt: at(8)})
	wantOutcome(t, out, err, usecases.OutcomeApplied)
	if o := observationOf(t, e.scorecard(t, "node-1:7d"), "f-1"); o.Kind != scorecard.KindFeedApplied || !o.At.Equal(at(8)) {
		t.Fatalf("observation = %+v", o)
	}
}

func TestEventsWithoutEffectOnlyWriteTheInbox(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	out, err := e.intake.Acknowledge(ctx, ref("v-1", 0))
	wantOutcome(t, out, err, usecases.OutcomeIgnored)
	wantCounts(t, e, memory.Counts{Processed: 1})
	out, err = e.intake.Acknowledge(ctx, ref("v-1", 0))
	wantOutcome(t, out, err, usecases.OutcomeDuplicate)
}

func TestASourceIdAlreadyCountedIsSuccess(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	first, err := scorecard.NewFeedApplied("f-1", at(8))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range scorecard.Windows() {
		s, err := scorecard.Open("node-1", w, first)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.store.Scorecards().Save(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	// the inbox has not seen the id, the aggregates have: the second line of defence
	out, err := e.intake.InventoryFeedApplied(ctx, usecases.InventoryFeedApplied{Ref: ref("f-1", 9), NodeID: "node-1", AppliedAt: at(8)})
	wantOutcome(t, out, err, usecases.OutcomeDuplicate)
	if s := e.scorecard(t, "node-1:7d"); s.Version() != 1 || s.ObservationCount() != 1 {
		t.Fatalf("a duplicate observation changed the scorecard: version %d", s.Version())
	}
}

func TestEventsThatCanNeverBeAppliedAreInvalidAndStoreNothing(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	for name, run := range map[string]func() error{
		"no event id": func() error {
			_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: usecases.EventRef{Type: "t"}, NodeID: "n", DeliveredAt: at(1)})
			return err
		},
		"no event type": func() error {
			_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: usecases.EventRef{ID: "e"}, NodeID: "n", DeliveredAt: at(1)})
			return err
		},
		"node with colon": func() error {
			_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("e", 1), NodeID: "a:b", DeliveredAt: at(1)})
			return err
		},
		"no node": func() error {
			_, err := e.intake.InventoryFeedApplied(ctx, usecases.InventoryFeedApplied{Ref: ref("e", 1), AppliedAt: at(1)})
			return err
		},
		"no deliveredAt": func() error {
			_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("e", 1), NodeID: "n"})
			return err
		},
		"no appliedAt": func() error {
			_, err := e.intake.InventoryFeedApplied(ctx, usecases.InventoryFeedApplied{Ref: ref("e", 1), NodeID: "n"})
			return err
		},
		"no ackBy": func() error {
			_, err := e.intake.PurchaseOrderExpired(ctx, usecases.PurchaseOrderExpired{Ref: ref("e", 1), PoNumber: "p", NodeID: "n"})
			return err
		},
		"facts no po": func() error {
			_, err := e.intake.PurchaseOrderIssued(ctx, usecases.PurchaseOrderIssued{Ref: ref("e", 1), NodeID: "n"})
			return err
		},
		"shipment no node": func() error {
			_, err := e.intake.ShipmentConfirmed(ctx, usecases.ShipmentConfirmed{Ref: ref("e", 1), PoNumber: "p"})
			return err
		},
	} {
		if err := run(); !errors.Is(err, usecases.ErrInvalidEvent) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent", name, err)
		}
	}
	wantCounts(t, e, memory.Counts{})
}

func TestAnInvalidHandoverInstantIsRefusedAfterTheFactsWereFound(t *testing.T) {
	e := newEnv()
	e.issue(t, "po-1", "node-1")
	_, err := e.intake.PackageHandedOver(context.Background(), usecases.PackageHandedOver{Ref: ref("ho-1", 5), PoNumber: "po-1", NodeID: "node-1"})
	if !errors.Is(err, usecases.ErrInvalidEvent) {
		t.Fatalf("err = %v", err)
	}
	_, err = e.intake.PurchaseOrderAcknowledged(context.Background(), usecases.PurchaseOrderAcknowledged{Ref: ref("ack-1", 5), PoNumber: "po-1", NodeID: "node-1"})
	if !errors.Is(err, usecases.ErrInvalidEvent) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortFailuresPassThroughUnchanged(t *testing.T) {
	ctx := context.Background()
	t.Run("inbox", func(t *testing.T) {
		e := newEnv(func(d *usecases.Deps) { d.Inbox = failingInbox{errBoom} })
		if _, err := e.intake.Acknowledge(ctx, ref("e", 0)); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("facts read", func(t *testing.T) {
		e := newEnv(func(d *usecases.Deps) { d.Facts = failingFacts{err: errBoom} })
		_, err := e.intake.PackageHandedOver(ctx, usecases.PackageHandedOver{Ref: ref("e", 1), PoNumber: "p", NodeID: "n", HandedOverAt: at(1)})
		if !errors.Is(err, errBoom) || errors.Is(err, usecases.ErrPurchaseOrderUnknown) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("scorecard read", func(t *testing.T) {
		e := newEnv(func(d *usecases.Deps) { d.Scorecards = failingGet{d.Scorecards, errBoom} })
		_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("e", 1), NodeID: "n", DeliveredAt: at(1)})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("scorecard save", func(t *testing.T) {
		e := newEnv(func(d *usecases.Deps) {
			d.Scorecards = &failingScorecards{ScorecardRepository: d.Scorecards, err: errBoom, n: 1}
		})
		_, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("e", 1), NodeID: "n", DeliveredAt: at(1)})
		if !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
		wantCounts(t, e, memory.Counts{})
	})
}

func TestAConcurrencyConflictIsRetriedOnFreshStateUpToFiveAttempts(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		conflicts int
		wantErr   bool
	}{{1, false}, {4, false}, {5, true}} {
		var failing *failingScorecards
		e := newEnv(func(d *usecases.Deps) {
			failing = &failingScorecards{ScorecardRepository: d.Scorecards, err: ports.ErrConcurrentUpdate, n: tc.conflicts}
			d.Scorecards = failing
		})
		out, err := e.intake.PromiseKept(ctx, usecases.PromiseKept{Ref: ref("e", 1), NodeID: "n", DeliveredAt: at(1)})
		if tc.wantErr {
			if !errors.Is(err, ports.ErrConcurrentUpdate) || failing.calls != 5 {
				t.Errorf("%d conflicts: err = %v after %d saves", tc.conflicts, err, failing.calls)
			}
			wantCounts(t, e, memory.Counts{})
			continue
		}
		wantOutcome(t, out, err, usecases.OutcomeApplied)
		// each retry reran the whole unit: the inbox row exists once, both scorecards exist
		wantCounts(t, e, memory.Counts{Scorecards: 2, Processed: 1, VersionSum: 2})
	}
}
