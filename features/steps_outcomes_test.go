package features_test

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/usecases"
)

func registerOutcomeSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the command succeeds$`, w.commandSucceeds)
	sc.Step(`^the command fails with "([^"]*)"$`, w.commandFailsWith)
	sc.Step(`^the outcome is "(applied|duplicate|ignored)"$`, w.outcomeIs)
	sc.Step(`^no event was published$`, w.noEventPublished)
	sc.Step(`^the events published were:$`, w.eventsPublished)
}

func (w *world) commandSucceeds() error {
	if w.lastErr != nil {
		return fmt.Errorf("the last command failed: %w", w.lastErr)
	}
	return nil
}

func (w *world) commandFailsWith(name string) error {
	want, err := errorNamed(name)
	if err != nil {
		return err
	}
	if !errors.Is(w.lastErr, want) {
		return fmt.Errorf("the last command returned %v, want %v", w.lastErr, want)
	}
	return nil
}

func (w *world) outcomeIs(want string) error {
	if err := w.commandSucceeds(); err != nil {
		return err
	}
	if w.lastOutcome != usecases.Outcome(want) {
		return fmt.Errorf("outcome = %q, want %q", w.lastOutcome, want)
	}
	return nil
}

func (w *world) noEventPublished() error {
	if got := w.published(); len(got) != 0 {
		return fmt.Errorf("events were published: %v", got)
	}
	return nil
}

func (w *world) eventsPublished(table *godog.Table) error {
	var want []string
	for _, row := range table.Rows {
		want = append(want, row.Cells[0].Value+" "+row.Cells[1].Value)
	}
	if got := w.published(); !reflect.DeepEqual(got, want) {
		return fmt.Errorf("published %v, want %v", got, want)
	}
	return nil
}
