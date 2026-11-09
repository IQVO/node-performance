package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// fastTierIneligibleName is the observable gauge of the fast-tier eligibility
// gate (ADR 0013). The Prometheus exporter renders the dotted name with
// underscores, `nodeperf_fast_tier_ineligible_nodes`; the unit `{node}` adds no
// suffix.
const fastTierIneligibleName = "nodeperf.fast_tier.ineligible_nodes"

// RegisterFastTierGauge registers the gauge of nodes currently suspended from
// the fast tiers. The value is read from count (the scorecards table) at each
// collection, so it is right after a restart and whichever replica runs the
// scheduler; a failing count skips that collection's data point rather than
// reporting a wrong zero. Call it after Setup so the instrument binds to the
// real MeterProvider.
func RegisterFastTierGauge(count func(context.Context) (int, error)) error {
	_, err := otel.Meter(meterName).Int64ObservableGauge(
		fastTierIneligibleName,
		metric.WithDescription("Nodes currently suspended from the SAME_DAY and NEXT_DAY tiers because the 7-day on-time handover "+
			"rate of their scorecard fell below 0.90 and has not yet stood at 0.95 or more on three distinct days. A rise means "+
			"delivery-promise stops sourcing fast tiers from more nodes; a gauge that stays high while the nodes keep reporting "+
			"on time means reinstatement is stuck (check the recompute scheduler)."),
		metric.WithUnit("{node}"),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			n, err := count(ctx)
			if err != nil {
				// No data point this time: a zero would read as "nobody is suspended".
				slog.WarnContext(ctx, "count nodes suspended from the fast tiers", "error", err)
				return nil
			}
			o.Observe(int64(n))
			return nil
		}),
	)
	return err
}
