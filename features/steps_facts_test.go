package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/usecases"
)

func registerFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^purchase order "([^"]*)" of node "([^"]*)" was issued with an ack window of (\d+) minutes and a required ship-by (\d+) minutes after the issue$`, w.issue)
	sc.Step(`^purchase order "([^"]*)" of node "([^"]*)" is (accepted|rejected) (\d+) minutes after the issue$`, w.acknowledge)
	sc.Step(`^purchase order "([^"]*)" of node "([^"]*)" expires$`, w.expire)
	sc.Step(`^the unknown purchase order "([^"]*)" of node "([^"]*)" is accepted$`, w.acceptUnknown)
	sc.Step(`^the package of purchase order "([^"]*)" of node "([^"]*)" is handed over (\d+) minutes after the issue$`, w.handOver)
	sc.Step(`^the vendor confirms the shipment of purchase order "([^"]*)" of node "([^"]*)" with a required ship-by in (\d+) minutes$`, w.confirmShipment)
	sc.Step(`^node "([^"]*)" has (\d+) on-time handovers and (\d+) late handovers$`, w.handovers)
	sc.Step(`^node "([^"]*)" has (\d+) kept promises and (\d+) promises broken by "([^"]*)"$`, w.promises)
	sc.Step(`^node "([^"]*)" has (\d+) purchase orders accepted early, (\d+) accepted late, (\d+) rejected and (\d+) expired$`, w.acknowledgements)
	sc.Step(`^node "([^"]*)" applied (\d+) inventory feeds, one every (\d+) minutes$`, w.feeds)
	sc.Step(`^the promise kept event "([^"]*)" for node "([^"]*)" is delivered (\d+) times$`, w.deliverKeptTwice)
	sc.Step(`^an event without effect "([^"]*)" is delivered$`, w.deliverWithoutEffect)
}

func (w *world) issue(po, node string, window, shipBy int) error {
	w.orders[po] = purchaseOrder{node: node, issuedAt: w.clock.Now(), ackWindow: time.Duration(window) * time.Minute, shipBy: time.Duration(shipBy) * time.Minute}
	o := w.orders[po]
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PurchaseOrderIssued(w.ctx(), usecases.PurchaseOrderIssued{
			Ref: w.ref(w.nextID("issued")), PoNumber: po, NodeID: node,
			AckBy: o.issuedAt.Add(o.ackWindow), RequiredShipBy: o.issuedAt.Add(o.shipBy),
		})
	})
	return w.lastErr
}

func (w *world) order(po string) (purchaseOrder, error) {
	o, ok := w.orders[po]
	if !ok {
		return purchaseOrder{}, fmt.Errorf("the scenario never issued purchase order %q", po)
	}
	return o, nil
}

func (w *world) acknowledge(po, node, verb string, minutes int) error {
	o, err := w.order(po)
	if err != nil {
		return err
	}
	at := o.issuedAt.Add(time.Duration(minutes) * time.Minute)
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PurchaseOrderAcknowledged(w.ctx(), usecases.PurchaseOrderAcknowledged{
			Ref: w.ref(w.nextID("ack")), PoNumber: po, NodeID: node, At: at, Rejected: verb == "rejected",
		})
	})
	return nil
}

func (w *world) expire(po, node string) error {
	o, err := w.order(po)
	if err != nil {
		return err
	}
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PurchaseOrderExpired(w.ctx(), usecases.PurchaseOrderExpired{
			Ref: w.ref(w.nextID("expired")), PoNumber: po, NodeID: node, AckBy: o.issuedAt.Add(o.ackWindow),
		})
	})
	return nil
}

func (w *world) handOver(po, node string, minutes int) error {
	return w.handOverAt(po, node, time.Duration(minutes)*time.Minute)
}

// handOverAt hands the package of po over at issue + after. An order the
// scenario never issued is handed over against the facts the service has
// (possibly none), at now + after.
func (w *world) handOverAt(po, node string, after time.Duration) error {
	at := w.clock.Now().Add(after)
	if o, ok := w.orders[po]; ok {
		at = o.issuedAt.Add(after)
	}
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PackageHandedOver(w.ctx(), usecases.PackageHandedOver{
			Ref: w.ref(w.nextID("handover")), PoNumber: po, NodeID: node, HandedOverAt: at,
		})
	})
	return nil
}

func (w *world) confirmShipment(po, node string, minutes int) error {
	w.run(func() (usecases.Outcome, error) {
		return w.intake.ShipmentConfirmed(w.ctx(), usecases.ShipmentConfirmed{
			Ref: w.ref(w.nextID("shipment")), PoNumber: po, NodeID: node,
			RequiredShipBy: w.clock.Now().Add(time.Duration(minutes) * time.Minute),
		})
	})
	return w.lastErr
}

// handovers records onTime handovers before and late ones after the required
// ship-by of purchase orders issued now with a 1800-minute ship-by.
func (w *world) handovers(node string, onTime, late int) error {
	for i := 0; i < onTime+late; i++ {
		po := w.nextID("po")
		if err := w.issue(po, node, 600, 1800); err != nil {
			return err
		}
		after := 1700 * time.Minute
		if i >= onTime {
			after = 1900 * time.Minute
		}
		if err := w.handOverAt(po, node, after); err != nil {
			return err
		}
		if w.lastErr != nil {
			return fmt.Errorf("handover %d: %w", i, w.lastErr)
		}
	}
	return nil
}

func (w *world) promises(node string, kept, broken int, cause string) error {
	for i := 0; i < kept; i++ {
		id := w.nextID("kept")
		if err := w.given("promise kept", func() (usecases.Outcome, error) {
			return w.intake.PromiseKept(w.ctx(), usecases.PromiseKept{Ref: w.ref(id), NodeID: node, DeliveredAt: w.clock.Now()})
		}); err != nil {
			return err
		}
	}
	for i := 0; i < broken; i++ {
		id := w.nextID("broken")
		w.run(func() (usecases.Outcome, error) {
			return w.intake.PromiseBroken(w.ctx(), usecases.PromiseBroken{Ref: w.ref(id), NodeID: node, At: w.clock.Now(), Cause: cause})
		})
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	return nil
}

// acknowledgements issues one purchase order per count and resolves it: early
// is accepted at minute 100 and late at minute 580 of a 600-minute window.
func (w *world) acknowledgements(node string, early, late, rejected, expired int) error {
	plan := []struct {
		n       int
		verb    string
		minutes int
		expire  bool
	}{{early, "accepted", 100, false}, {late, "accepted", 580, false}, {rejected, "rejected", 100, false}, {expired, "", 0, true}}
	for _, p := range plan {
		for i := 0; i < p.n; i++ {
			po := w.nextID("po")
			if err := w.issue(po, node, 600, 1800); err != nil {
				return err
			}
			var err error
			if p.expire {
				err = w.expire(po, node)
			} else {
				err = w.acknowledge(po, node, p.verb, p.minutes)
			}
			if err != nil {
				return err
			}
			if w.lastErr != nil {
				return w.lastErr
			}
		}
	}
	return nil
}

func (w *world) feeds(node string, n, everyMinutes int) error {
	start := w.clock.Now()
	for i := 0; i < n; i++ {
		at := start.Add(time.Duration(i*everyMinutes) * time.Minute)
		id := w.nextID("feed")
		if err := w.given("inventory feed", func() (usecases.Outcome, error) {
			return w.intake.InventoryFeedApplied(w.ctx(), usecases.InventoryFeedApplied{Ref: w.ref(id), NodeID: node, AppliedAt: at})
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) deliverKeptTwice(id, node string, times int) error {
	for i := 0; i < times; i++ {
		w.run(func() (usecases.Outcome, error) {
			return w.intake.PromiseKept(w.ctx(), usecases.PromiseKept{Ref: w.ref(id), NodeID: node, DeliveredAt: w.clock.Now()})
		})
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	return nil
}

func (w *world) deliverWithoutEffect(id string) error {
	w.run(func() (usecases.Outcome, error) { return w.intake.Acknowledge(w.ctx(), w.ref(id)) })
	return w.lastErr
}

// acceptUnknown accepts a purchase order whose issue the service never saw.
func (w *world) acceptUnknown(po, node string) error {
	w.run(func() (usecases.Outcome, error) {
		return w.intake.PurchaseOrderAcknowledged(w.ctx(), usecases.PurchaseOrderAcknowledged{
			Ref: w.ref(w.nextID("ack")), PoNumber: po, NodeID: node, At: w.clock.Now(),
		})
	})
	return nil
}
