package features_test

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
)

func registerScorecardSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the scorecard "([^"]*)" does not exist$`, w.scorecardDoesNotExist)
	sc.Step(`^the scorecard "([^"]*)" is (GRADED|PROVISIONAL)$`, w.scorecardIs)
	sc.Step(`^the scorecard "([^"]*)" is GRADED with grade "([A-D])"$`, w.scorecardGraded)
	sc.Step(`^the scorecard "([^"]*)" has not been computed yet$`, w.notComputed)
	sc.Step(`^the scorecard "([^"]*)" holds (\d+) observations$`, w.holdsObservations)
	sc.Step(`^the scorecard "([^"]*)" has version (\d+)$`, w.hasVersion)
	sc.Step(`^the scorecard "([^"]*)" counts (\d+) observations$`, w.countsObservations)
	sc.Step(`^the metric "([^"]*)" of the scorecard "([^"]*)" is ([0-9.]+)$`, w.metricIs)
	sc.Step(`^the scorecard "([^"]*)" is read as grade "([^"]*)"$`, w.readAsGrade)
	sc.Step(`^listing the grade "([^"]*)" returns "([^"]*)"$`, w.listing)
	sc.Step(`^listing the grade "([^"]*)" is refused as invalid$`, w.listingRefused)
}

// viewOf reads one scorecard through the query use case.
func (w *world) viewOf(id string) (views.Scorecard, error) {
	node, window, ok := strings.Cut(id, ":")
	if !ok {
		return views.Scorecard{}, fmt.Errorf("%q is not a scorecardId", id)
	}
	got, err := w.queries.GetScorecard(w.ctx(), node)
	if err != nil {
		return views.Scorecard{}, fmt.Errorf("GetScorecard(%s): %w", node, err)
	}
	for _, v := range got.Scorecards {
		if v.Window == window {
			return v, nil
		}
	}
	return views.Scorecard{}, fmt.Errorf("node %s has no %s scorecard", node, window)
}

func (w *world) scorecardDoesNotExist(id string) error {
	if _, err := w.viewOf(id); err == nil {
		return fmt.Errorf("scorecard %s exists", id)
	}
	return nil
}

func (w *world) scorecardIs(id, state string) error {
	v, err := w.viewOf(id)
	if err != nil {
		return err
	}
	if v.State != state {
		return fmt.Errorf("%s is %s, want %s", id, v.State, state)
	}
	if state == "PROVISIONAL" && v.Grade != "PROVISIONAL" {
		return fmt.Errorf("%s is PROVISIONAL but reads as grade %s", id, v.Grade)
	}
	return nil
}

func (w *world) scorecardGraded(id, grade string) error {
	if err := w.scorecardIs(id, "GRADED"); err != nil {
		return err
	}
	return w.readAsGrade(id, grade)
}

func (w *world) readAsGrade(id, grade string) error {
	v, err := w.viewOf(id)
	if err != nil {
		return err
	}
	if v.Grade != grade {
		return fmt.Errorf("%s reads as grade %s, want %s", id, v.Grade, grade)
	}
	return nil
}

func (w *world) notComputed(id string) error {
	v, err := w.viewOf(id)
	if err != nil {
		return err
	}
	if !v.ComputedAt.IsZero() {
		return fmt.Errorf("%s was computed at %v", id, v.ComputedAt)
	}
	return nil
}

func (w *world) holdsObservations(id string, want int) error {
	s, err := w.store.Scorecards().Get(w.ctx(), id)
	if err != nil {
		return err
	}
	if s.ObservationCount() != want {
		return fmt.Errorf("%s holds %d observations, want %d", id, s.ObservationCount(), want)
	}
	return nil
}

func (w *world) hasVersion(id string, want int) error {
	s, err := w.store.Scorecards().Get(w.ctx(), id)
	if err != nil {
		return err
	}
	if s.Version() != int64(want) {
		return fmt.Errorf("%s is at version %d, want %d", id, s.Version(), want)
	}
	return nil
}

func (w *world) countsObservations(id string, want int) error {
	v, err := w.viewOf(id)
	if err != nil {
		return err
	}
	if v.Metrics.Observations != want {
		return fmt.Errorf("%s counts %d observations, want %d", id, v.Metrics.Observations, want)
	}
	return nil
}

func (w *world) metricIs(metric, id, want string) error {
	v, err := w.viewOf(id)
	if err != nil {
		return err
	}
	wanted, err := strconv.ParseFloat(want, 64)
	if err != nil {
		return err
	}
	got, ok := map[string]float64{
		"onTimeHandoverRate": v.Metrics.OnTimeHandoverRate, "lateAckRate": v.Metrics.LateAckRate,
		"rejectRate": v.Metrics.RejectRate, "expiryRate": v.Metrics.ExpiryRate,
		"promiseKeptRate": v.Metrics.PromiseKeptRate, "feedFreshnessP95Minutes": float64(v.Metrics.FeedFreshnessP95Minutes),
	}[metric]
	if !ok {
		return fmt.Errorf("unknown metric %q", metric)
	}
	if math.Abs(got-wanted) > 1e-9 {
		return fmt.Errorf("%s of %s is %v, want %v", metric, id, got, wanted)
	}
	return nil
}

func (w *world) listing(grade, want string) error {
	page, err := w.queries.ListScorecardsByGrade(w.ctx(), usecases.ListByGradeInput{Grade: grade})
	if err != nil {
		return err
	}
	ids := make([]string, len(page.Items))
	for i, v := range page.Items {
		ids[i] = v.ScorecardID
	}
	if got := strings.Join(ids, ","); got != want {
		return fmt.Errorf("grade %s lists %q, want %q", grade, got, want)
	}
	return nil
}

func (w *world) listingRefused(grade string) error {
	if _, err := w.queries.ListScorecardsByGrade(w.ctx(), usecases.ListByGradeInput{Grade: grade}); err == nil {
		return fmt.Errorf("grade %q was accepted", grade)
	}
	return nil
}
