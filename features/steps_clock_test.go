package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"
)

func registerClockSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the clock advances by (\d+) (minutes|hours|days)$`, w.clockAdvances)
	sc.Step(`^the scorecards are recomputed$`, w.recomputeAll)
}

func (w *world) clockAdvances(n int, unit string) error {
	unitLength := map[string]time.Duration{"minutes": time.Minute, "hours": time.Hour, "days": 24 * time.Hour}[unit]
	w.clock.Advance(time.Duration(n) * unitLength)
	return nil
}

func (w *world) recomputeAll() error {
	w.outboxBefore = len(w.store.Outbox().Events())
	if _, err := w.recompute.Run(w.ctx()); err != nil {
		return fmt.Errorf("recompute: %w", err)
	}
	w.lastOutcome, w.lastErr = "", nil
	return nil
}
