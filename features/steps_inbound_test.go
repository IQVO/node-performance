package features_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
)

func registerInboundSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^shipment "([^"]*)" of node "([^"]*)" shipped (\d+) minutes ago$`, w.shipmentShipped)
	sc.Step(`^the receipt of shipment "([^"]*)" at node "([^"]*)" is reported with cartons "([^"]*)" received (\d+) minutes ago$`, w.receiptReported)
	sc.Step(`^the receipt event "([^"]*)" of shipment "([^"]*)" at node "([^"]*)" is delivered (\d+) times with cartons "([^"]*)" received (\d+) minutes ago$`, w.receiptDelivered)
	sc.Step(`^the scorecard "([^"]*)" shows no inbound metrics$`, w.noInbound)
	sc.Step(`^the scorecard "([^"]*)" shows inbound timeliness p95 of (\d+) minutes$`, w.inboundTimeliness)
	sc.Step(`^the scorecard "([^"]*)" shows inbound accuracy of ([0-9.]+)$`, w.inboundAccuracy)
}

func (w *world) shipmentShipped(shipment, node string, minutesAgo int) error {
	w.run(func() (usecases.Outcome, error) {
		return w.intake.InboundShipmentShipped(w.ctx(), usecases.InboundShipmentShipped{
			Ref: w.ref(w.nextID("shipped")), ShipmentID: shipment, NodeID: node,
			ShippedAt: w.clock.Now().Add(-time.Duration(minutesAgo) * time.Minute),
		})
	})
	return w.lastErr
}

func (w *world) deliverReceipt(id, shipment, node, cartons string, minutesAgo int) {
	var statuses []string
	if cartons != "" {
		statuses = strings.Split(cartons, ",")
	}
	w.run(func() (usecases.Outcome, error) {
		return w.intake.InboundReceiptSubmitted(w.ctx(), usecases.InboundReceiptSubmitted{
			Ref: w.ref(id), ShipmentID: shipment, NodeID: node, CartonStatuses: statuses,
			ReceivedAt: w.clock.Now().Add(-time.Duration(minutesAgo) * time.Minute),
		})
	})
}

func (w *world) receiptReported(shipment, node, cartons string, minutesAgo int) error {
	w.deliverReceipt(w.nextID("receipt"), shipment, node, cartons, minutesAgo)
	return w.lastErr
}

func (w *world) receiptDelivered(id, shipment, node string, times int, cartons string, minutesAgo int) error {
	for i := 0; i < times; i++ {
		w.deliverReceipt(id, shipment, node, cartons, minutesAgo)
		if w.lastErr != nil {
			return w.lastErr
		}
	}
	return nil
}

func (w *world) scorecardView(id string) (views.Scorecard, error) {
	node, _, _ := strings.Cut(id, ":")
	got, err := w.queries.GetScorecard(w.ctx(), node)
	if err != nil {
		return views.Scorecard{}, err
	}
	for _, s := range got.Scorecards {
		if s.ScorecardID == id {
			return s, nil
		}
	}
	return views.Scorecard{}, fmt.Errorf("the scorecard %q is not read", id)
}

func (w *world) noInbound(id string) error {
	s, err := w.scorecardView(id)
	if err != nil {
		return err
	}
	if s.Metrics.InboundTimelinessP95Minutes != nil || s.Metrics.InboundAccuracyRate != nil {
		return fmt.Errorf("%s shows inbound metrics: %v %v", id, s.Metrics.InboundTimelinessP95Minutes, s.Metrics.InboundAccuracyRate)
	}
	return nil
}

func (w *world) inboundTimeliness(id string, want int) error {
	s, err := w.scorecardView(id)
	if err != nil {
		return err
	}
	if got := s.Metrics.InboundTimelinessP95Minutes; got == nil || *got != want {
		return fmt.Errorf("%s inboundTimelinessP95Minutes = %v, want %d", id, got, want)
	}
	return nil
}

func (w *world) inboundAccuracy(id string, want float64) error {
	s, err := w.scorecardView(id)
	if err != nil {
		return err
	}
	if got := s.Metrics.InboundAccuracyRate; got == nil || *got != want {
		return fmt.Errorf("%s inboundAccuracyRate = %v, want %v", id, got, want)
	}
	return nil
}
