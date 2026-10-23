package features_test

import (
	"fmt"
	"math"
	"strconv"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// registerEventPayloadSteps registers the steps that assert WHAT the
// published events carry, not only that they were published: the grade of
// NodeScorecardUpdated and the metric, value and threshold of
// NodeThresholdBreached (.claude/rules/integration-events.md).
func registerEventPayloadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^a "([^"]*)" event was published for "([^"]*)" carrying the grade "([^"]*)"$`, w.eventCarriesGrade)
	sc.Step(`^a "([^"]*)" event was published for "([^"]*)" naming the metric "([^"]*)" with value ([0-9.]+) against the threshold ([0-9.]+)$`, w.eventNamesBreach)
}

// lastEvent returns the newest event of name for subject in the outbox.
func (w *world) lastEvent(name, subject string) (shared.Event, error) {
	events := w.store.Outbox().Events()
	for i := len(events) - 1; i >= 0; i-- {
		if e := events[i]; e.EventName() == name && e.SubjectID() == subject {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no %s event for %s was published", name, subject)
}

func (w *world) eventCarriesGrade(name, subject, grade string) error {
	e, err := w.lastEvent(name, subject)
	if err != nil {
		return err
	}
	u, ok := e.(scorecard.NodeScorecardUpdated)
	if !ok {
		return fmt.Errorf("the %s event of %s is a %T, want a NodeScorecardUpdated", name, subject, e)
	}
	if got := u.GradeLabel(); got != grade {
		return fmt.Errorf("the %s event of %s carries the grade %s, want %s", name, subject, got, grade)
	}
	return nil
}

func (w *world) eventNamesBreach(name, subject, metric, value, threshold string) error {
	e, err := w.lastEvent(name, subject)
	if err != nil {
		return err
	}
	b, ok := e.(scorecard.NodeThresholdBreached)
	if !ok {
		return fmt.Errorf("the %s event of %s is a %T, want a NodeThresholdBreached", name, subject, e)
	}
	if string(b.Metric) != metric {
		return fmt.Errorf("the %s event of %s names the metric %s, want %s", name, subject, b.Metric, metric)
	}
	wantValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return err
	}
	wantThreshold, err := strconv.ParseFloat(threshold, 64)
	if err != nil {
		return err
	}
	if math.Abs(b.Value-wantValue) > 1e-9 {
		return fmt.Errorf("the %s event of %s carries the value %v, want %v", name, subject, b.Value, wantValue)
	}
	if math.Abs(b.Threshold-wantThreshold) > 1e-9 {
		return fmt.Errorf("the %s event of %s carries the threshold %v, want %v", name, subject, b.Threshold, wantThreshold)
	}
	return nil
}
