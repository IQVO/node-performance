package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/node-performance/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/node-performance"

// scorecardsRecomputedName is the Tier 2 business metric of the context (ADR
// 0010, the fleet's standard metrics convention): every scorecard the Recompute
// command handled, by outcome.
const scorecardsRecomputedName = "nodeperformance.scorecards.recomputed"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeRecomputed = "recomputed"
	outcomeFailed     = "failed"
)

// ScorecardMetrics implements ports.ScorecardMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type ScorecardMetrics struct {
	counter metric.Int64Counter
}

var _ ports.ScorecardMetrics = (*ScorecardMetrics)(nil)

// NewScorecardMetrics registers the counter. It only fails if the instrument
// name is invalid, which is a programming error, not a runtime condition. Call
// it after Setup so the instrument binds to the real MeterProvider.
func NewScorecardMetrics() (*ScorecardMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		scorecardsRecomputedName,
		metric.WithDescription("Scorecards handled by the recompute scheduler, by outcome (recomputed or failed; reason says why a "+
			"failure happened: concurrent-update or internal-error). A recomputed count that stays flat while nodes keep reporting "+
			"means grading has stalled and published grades are going stale; a rising concurrent-update means writers contend for "+
			"the same scorecards but the next run retries them; a rising internal-error means the service itself (usually its "+
			"database) is failing."),
		metric.WithUnit("{scorecard}"),
	)
	if err != nil {
		return nil, err
	}
	return &ScorecardMetrics{counter: counter}, nil
}

// ScorecardRecomputed counts a scorecard that was recomputed and saved.
func (m *ScorecardMetrics) ScorecardRecomputed(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRecomputed)))
}

// ScorecardRecomputeFailed counts a scorecard that was not recomputed, with the
// reason why.
func (m *ScorecardMetrics) ScorecardRecomputeFailed(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeFailed), reasonKey.String(reason)))
}
