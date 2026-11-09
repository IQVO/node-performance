package features_test

import (
	"fmt"
	"math"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// registerTierSteps registers the steps of the fast-tier eligibility feature
// (ADR 0013): the state the 7d scorecard holds and what the flip published.
func registerTierSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^node "([^"]*)" is (eligible|ineligible) for the fast tiers$`, w.eligibleIs)
	sc.Step(`^the reinstatement streak of node "([^"]*)" is (\d+)$`, w.streakIs)
	sc.Step(`^a tier event was published for "([^"]*)" for the tier "(SAME_DAY|NEXT_DAY)" with eligible (true|false) on value ([0-9.]+) against the threshold ([0-9.]+)$`, w.tierEventCarries)
}

func (w *world) fastTierOf(node string) (scorecard.FastTierState, error) {
	s, err := w.store.Scorecards().Get(w.ctx(), node+":7d")
	if err != nil {
		return scorecard.FastTierState{}, err
	}
	return s.FastTier(), nil
}

func (w *world) eligibleIs(node, want string) error {
	got, err := w.fastTierOf(node)
	if err != nil {
		return err
	}
	if got.Eligible() != (want == "eligible") {
		return fmt.Errorf("node %s is eligible=%v for the fast tiers, want %s", node, got.Eligible(), want)
	}
	return nil
}

func (w *world) streakIs(node string, want int) error {
	got, err := w.fastTierOf(node)
	if err != nil {
		return err
	}
	if got.Streak != want {
		return fmt.Errorf("the reinstatement streak of %s is %d, want %d", node, got.Streak, want)
	}
	return nil
}

func (w *world) tierEventCarries(subject, tier, eligible, value, threshold string) error {
	wantValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	wantThreshold, err := strconv.ParseFloat(threshold, 64)
	if err != nil {
		return err
	}
	events := w.store.Outbox().Events()
	for i := len(events) - 1; i >= 0; i-- {
		e, ok := events[i].(scorecard.NodeTierEligibilityChanged)
		if !ok || e.SubjectID() != subject || string(e.Tier) != tier {
			continue
		}
		if strconv.FormatBool(e.Eligible) != eligible || e.Metric != scorecard.MetricOnTimeHandoverRate || e.Window != scorecard.Window7d ||
			math.Abs(e.Value-wantValue) > 1e-9 || math.Abs(e.Threshold-wantThreshold) > 1e-9 || !e.ChangedAt.Equal(w.clock.Now()) {
			return fmt.Errorf("the newest %s tier event of %s is %+v", tier, subject, e)
		}
		return nil
	}
	return fmt.Errorf("no NodeTierEligibilityChanged for %s and the tier %s was published", subject, tier)
}
