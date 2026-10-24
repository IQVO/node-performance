// Package features_test hosts the godog (Cucumber for Go) acceptance suite of
// node-performance. Every scenario in this directory drives the application's
// use cases, the ones the Kafka consumers, the recompute scheduler and the REST
// adapter call, over the in-memory doubles (test doubles: the shipped binary
// runs on Postgres). `make bdd` runs it with `go test ./... -run TestFeatures`;
// the runner lives here, not at the module root, because the root holds no Go
// file.
package features_test

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
)

// TestFeatures runs every Gherkin feature in this directory. Strict makes an
// undefined or pending step fail the suite instead of passing silently.
func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"."},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}

// initializeScenario gives every scenario a fresh world and registers the
// steps of every feature area.
func initializeScenario(sc *godog.ScenarioContext) {
	w := newWorld()
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		w.reset()
		return ctx, nil
	})
	registerClockSteps(sc, w)
	registerFactSteps(sc, w)
	registerOutcomeSteps(sc, w)
	registerScorecardSteps(sc, w)
	registerReadSteps(sc, w)
	registerEventPayloadSteps(sc, w)
	registerInboundSteps(sc, w)
}
