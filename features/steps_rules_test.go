package features_test

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// registerRuleSteps registers the steps of the documentation-derived rule
// features (*_rules.feature): deliveries with explicit ids and boundary node
// ids, feed cadences, the metrics a recompute breached, the payload of the
// published events and the not-found and cursor answers of the reads.
func registerRuleSteps(sc *godog.ScenarioContext, w *world) {
	registerRuleDeliverySteps(sc, w)
	registerRuleFactSteps(sc, w)
	registerRuleAssertionSteps(sc, w)
	registerRuleReadSteps(sc, w)
}

func registerRuleDeliverySteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a promise kept event "([^"]*)" is delivered for node "([^"]*)"$`, w.deliverKeptFor)
	sc.Step(`^a promise kept event "([^"]*)" without a delivery instant is delivered for node "([^"]*)"$`, w.deliverKeptWithoutInstant)
	sc.Step(`^a promise kept for a node whose id has (\d+) characters is delivered$`, w.deliverKeptForLongNode)
	sc.Step(`^a promise kept event whose id has (\d+) characters is delivered for node "([^"]*)"$`, w.deliverKeptWithLongID)
	sc.Step(`^the delivery is (applied|refused as an invalid event)$`, w.deliveryIs)
	sc.Step(`^the (package handover|purchase order acceptance|purchase order expiry|inventory feed|promise broken by LATE|promise broken by LOST|promise broken by RETURNED) event "([^"]*)" for node "([^"]*)" is delivered (\d+) times?$`, w.deliverKindTimes)
	sc.Step(`^the package of purchase order "([^"]*)" of node "([^"]*)" is handed over (\d+) minutes after the issue as event "([^"]*)"$`, w.handOverAs)
	sc.Step(`^the purchase order "([^"]*)" of node "([^"]*)" expires without its issue having been seen$`, w.expireUnseen)
}

func registerRuleFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a promise broken by "([^"]*)" for node "([^"]*)" without a delivery instant is stamped (\d+) days ago$`, w.brokenStampedDaysAgo)
	sc.Step(`^node "([^"]*)" applied feeds with the gaps "([^"]*)"$`, w.feedsWithGaps)
	sc.Step(`^node "([^"]*)" applied inventory feeds at the minutes "([^"]*)"$`, w.feedsAtMinutes)
	sc.Step(`^node "([^"]*)" received (\d+) shipments that took (\d+) minutes each$`, w.receivedShipments)
}

func registerRuleAssertionSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the metrics breached for "([^"]*)" were "([^"]*)"$`, w.metricsBreached)
	sc.Step(`^the "NodeScorecardUpdated" event for "([^"]*)" carries (\d+) observations and an on-time handover rate of ([0-9.]+) computed at the current clock$`, w.updatedCarries)
	sc.Step(`^the "NodeThresholdBreached" event for "([^"]*)" is about the node "([^"]*)" in the window "([^"]*)"$`, w.breachIsAbout)
}

func registerRuleReadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^reading the node "([^"]*)" is not found$`, w.readingNodeNotFound)
	sc.Step(`^reading a node whose id has (\d+) characters is (not found|refused as invalid)$`, w.readingLongNode)
	sc.Step(`^listing after a cursor of (\d+) characters returns "([^"]*)"$`, w.listingAfterLongCursor)
	sc.Step(`^the scorecard "([^"]*)" shows no inbound accuracy$`, w.noInboundAccuracy)
	sc.Step(`^listing the grade "([^"]*)" shows inbound accuracy of ([0-9.]+) on "([^"]*)"$`, w.listingShowsAccuracy)
	sc.Step(`^listing the grade "([^"]*)" shows inbound timeliness p95 of (\d+) minutes on "([^"]*)"$`, w.listingShowsTimeliness)
}

// --- deliveries -----------------------------------------------------------

func (w *world) keptEvent(ref usecases.EventRef, node string, at time.Time) {
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PromiseKept(w.ctx(), usecases.PromiseKept{Ref: ref, NodeID: node, DeliveredAt: at})
	})
}

func (w *world) deliverKeptFor(id, node string) error {
	w.keptEvent(w.ref(id), node, w.clock.Now())
	return nil
}

func (w *world) deliverKeptWithoutInstant(id, node string) error {
	w.keptEvent(w.ref(id), node, time.Time{})
	return nil
}

func (w *world) deliverKeptForLongNode(n int) error {
	w.keptEvent(w.ref(w.nextID("kept")), strings.Repeat("n", n), w.clock.Now())
	return nil
}

func (w *world) deliverKeptWithLongID(n int, node string) error {
	w.keptEvent(w.ref(strings.Repeat("e", n)), node, w.clock.Now())
	return nil
}

func (w *world) deliveryIs(want string) error {
	if want == "applied" {
		return w.outcomeIs("applied")
	}
	return w.commandFailsWith("invalid event")
}

// deliverKindTimes delivers one event of the named kind, with the same
// CloudEvents id, times times. Acknowledgements and handovers first issue the
// purchase order they are about, so the facts to judge them exist.
func (w *world) deliverKindTimes(kind, id, node string, times int) error {
	po := "po-" + id
	if kind == "package handover" || kind == "purchase order acceptance" {
		if err := w.issue(po, node, 600, 1800); err != nil {
			return err
		}
	}
	for i := 0; i < times; i++ {
		w.run(func() (usecases.Outcome, error) { return w.deliverOnce(kind, id, node, po) })
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	return nil
}

func (w *world) deliverOnce(kind, id, node, po string) (usecases.Outcome, error) {
	now := w.clock.Now()
	switch {
	case kind == "package handover":
		return w.intake.PackageHandedOver(w.ctx(), usecases.PackageHandedOver{
			Ref: w.ref(id), PoNumber: po, NodeID: node, HandedOverAt: w.orders[po].issuedAt.Add(100 * time.Minute)})
	case kind == "purchase order acceptance":
		return w.intake.PurchaseOrderAcknowledged(w.ctx(), usecases.PurchaseOrderAcknowledged{
			Ref: w.ref(id), PoNumber: po, NodeID: node, At: w.orders[po].issuedAt.Add(100 * time.Minute)})
	case kind == "purchase order expiry":
		return w.intake.PurchaseOrderExpired(w.ctx(), usecases.PurchaseOrderExpired{Ref: w.ref(id), PoNumber: po, NodeID: node, AckBy: now})
	case kind == "inventory feed":
		return w.intake.InventoryFeedApplied(w.ctx(), usecases.InventoryFeedApplied{Ref: w.ref(id), NodeID: node, AppliedAt: now})
	case strings.HasPrefix(kind, "promise broken by "):
		return w.intake.PromiseBroken(w.ctx(), usecases.PromiseBroken{
			Ref: w.ref(id), NodeID: node, At: now, Cause: strings.TrimPrefix(kind, "promise broken by ")})
	}
	return "", fmt.Errorf("the scenario names an unknown event kind: %s", kind)
}

func (w *world) handOverAs(po, node string, minutes int, id string) error {
	at := w.clock.Now().Add(time.Duration(minutes) * time.Minute)
	if o, ok := w.orders[po]; ok {
		at = o.issuedAt.Add(time.Duration(minutes) * time.Minute)
	}
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PackageHandedOver(w.ctx(), usecases.PackageHandedOver{Ref: w.ref(id), PoNumber: po, NodeID: node, HandedOverAt: at})
	})
	return nil
}

func (w *world) expireUnseen(po, node string) error {
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PurchaseOrderExpired(w.ctx(), usecases.PurchaseOrderExpired{
			Ref: w.ref(w.nextID("expired")), PoNumber: po, NodeID: node, AckBy: w.clock.Now()})
	})
	return nil
}

// --- facts ----------------------------------------------------------------

func (w *world) brokenStampedDaysAgo(cause, node string, days int) error {
	ref := w.ref(w.nextID("broken"))
	ref.Time = w.clock.Now().Add(-time.Duration(days) * 24 * time.Hour)
	return w.given("a promise broken without a delivery instant", func() (usecases.Outcome, error) {
		return w.intake.PromiseBroken(w.ctx(), usecases.PromiseBroken{Ref: ref, NodeID: node, Cause: cause})
	})
}

func (w *world) feedAt(node string, at time.Time) error {
	id := w.nextID("feed")
	return w.given("inventory feed", func() (usecases.Outcome, error) {
		return w.intake.InventoryFeedApplied(w.ctx(), usecases.InventoryFeedApplied{Ref: w.ref(id), NodeID: node, AppliedAt: at})
	})
}

// feedsWithGaps applies a first feed now and then one feed per gap; the gaps
// are "<duration>x<count>" groups, for example "10mx19,300mx1".
func (w *world) feedsWithGaps(node, gaps string) error {
	at := w.clock.Now()
	if err := w.feedAt(node, at); err != nil {
		return err
	}
	for _, group := range strings.Split(gaps, ",") {
		cut := strings.LastIndex(group, "x")
		if cut < 0 {
			return fmt.Errorf("the gap group %q is not <duration>x<count>", group)
		}
		gap, err := time.ParseDuration(group[:cut])
		if err != nil {
			return err
		}
		count, err := strconv.Atoi(group[cut+1:])
		if err != nil {
			return err
		}
		for i := 0; i < count; i++ {
			at = at.Add(gap)
			if err := w.feedAt(node, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// feedsAtMinutes applies one feed per listed minute offset from now, in the
// order listed (which need not be chronological).
func (w *world) feedsAtMinutes(node, minutes string) error {
	for _, m := range strings.Split(minutes, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(m))
		if err != nil {
			return err
		}
		if err := w.feedAt(node, w.clock.Now().Add(time.Duration(n)*time.Minute)); err != nil {
			return err
		}
	}
	return nil
}

// receivedShipments reports n shipments, each shipped minutes before its
// receipt, the receipt having arrived one minute ago with one RECEIVED carton.
func (w *world) receivedShipments(node string, n, minutes int) error {
	for i := 0; i < n; i++ {
		shipment := w.nextID("bulk")
		if err := w.shipmentShipped(shipment, node, minutes+1); err != nil {
			return err
		}
		if err := w.receiptReported(shipment, node, "RECEIVED", 1); err != nil {
			return err
		}
	}
	return nil
}

// --- assertions on what was published -------------------------------------

func (w *world) metricsBreached(subject, want string) error {
	var got []string
	for _, e := range w.store.Outbox().Events()[w.outboxBefore:] {
		if b, ok := e.(scorecard.NodeThresholdBreached); ok && b.SubjectID() == subject {
			got = append(got, string(b.Metric))
		}
	}
	if joined := strings.Join(got, ","); joined != want {
		return fmt.Errorf("the recompute breached %q for %s, want %q", joined, subject, want)
	}
	return nil
}

func (w *world) updatedCarries(subject string, observations int, onTime string) error {
	e, err := w.lastEvent("NodeScorecardUpdated", subject)
	if err != nil {
		return err
	}
	u, ok := e.(scorecard.NodeScorecardUpdated)
	if !ok {
		return fmt.Errorf("the event of %s is a %T, want a NodeScorecardUpdated", subject, e)
	}
	want, err := strconv.ParseFloat(onTime, 64)
	if err != nil {
		return err
	}
	switch {
	case u.SubjectID() != subject:
		return fmt.Errorf("the event is about %s, want %s", u.SubjectID(), subject)
	case u.Metrics.Observations != observations:
		return fmt.Errorf("%s carries %d observations, want %d", subject, u.Metrics.Observations, observations)
	case math.Abs(u.Metrics.OnTimeHandoverRate-want) > 1e-9:
		return fmt.Errorf("%s carries an on-time handover rate of %v, want %v", subject, u.Metrics.OnTimeHandoverRate, want)
	case !u.ComputedAt.Equal(w.clock.Now()):
		return fmt.Errorf("%s was computed at %v, want the recompute instant %v", subject, u.ComputedAt, w.clock.Now())
	}
	return nil
}

func (w *world) breachIsAbout(subject, node, window string) error {
	e, err := w.lastEvent("NodeThresholdBreached", subject)
	if err != nil {
		return err
	}
	b, ok := e.(scorecard.NodeThresholdBreached)
	if !ok {
		return fmt.Errorf("the event of %s is a %T, want a NodeThresholdBreached", subject, e)
	}
	if string(b.NodeID) != node || string(b.Window) != window {
		return fmt.Errorf("%s is about node %s in window %s, want %s in %s", subject, b.NodeID, b.Window, node, window)
	}
	return nil
}

// --- reads ----------------------------------------------------------------

func (w *world) readingNodeNotFound(node string) error {
	_, err := w.queries.GetScorecard(w.ctx(), node)
	if !errors.Is(err, ports.ErrNotFound) {
		return fmt.Errorf("reading node %q = %v, want not found", node, err)
	}
	return nil
}

func (w *world) readingLongNode(n int, answer string) error {
	node := strings.Repeat("n", n)
	if answer == "not found" {
		return w.readingNodeNotFound(node)
	}
	return w.readingNodeRefused(node)
}

func (w *world) listingAfterLongCursor(n int, want string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: "A", After: strings.Repeat("a", n)})
	if err != nil {
		return err
	}
	return wantIDs(page, want)
}

func (w *world) noInboundAccuracy(id string) error {
	s, err := w.scorecardView(id)
	if err != nil {
		return err
	}
	if s.Metrics.InboundAccuracyRate != nil {
		return fmt.Errorf("%s shows an inbound accuracy of %v", id, *s.Metrics.InboundAccuracyRate)
	}
	return nil
}

func (w *world) listingShowsAccuracy(grade string, want float64, id string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: grade})
	if err != nil {
		return err
	}
	for _, s := range page.Items {
		if s.ScorecardID == id {
			if got := s.Metrics.InboundAccuracyRate; got == nil || *got != want {
				return fmt.Errorf("the listed %s shows inbound accuracy %v, want %v", id, got, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the grade %s does not list %s", grade, id)
}

func (w *world) listingShowsTimeliness(grade string, want int, id string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: grade})
	if err != nil {
		return err
	}
	for _, s := range page.Items {
		if s.ScorecardID == id {
			if got := s.Metrics.InboundTimelinessP95Minutes; got == nil || *got != want {
				return fmt.Errorf("the listed %s shows inbound timeliness %v, want %d", id, got, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the grade %s does not list %s", grade, id)
}
