package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/node-performance/internal/adapters/outbound/telemetry"
)

func TestScorecardMetricsCountsByOutcomeAndReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewScorecardMetrics()
	if err != nil {
		t.Fatalf("NewScorecardMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.ScorecardRecomputed(ctx)
	metrics.ScorecardRecomputed(ctx)
	metrics.ScorecardRecomputeFailed(ctx, "concurrent-update")
	metrics.ScorecardRecomputeFailed(ctx, "internal-error")
	metrics.ScorecardRecomputeFailed(ctx, "internal-error")

	got := collectScorecardPoints(t, reader)
	want := map[string]int64{"recomputed/": 2, "failed/concurrent-update": 1, "failed/internal-error": 2}
	if len(got) != len(want) {
		t.Fatalf("data points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}

// collectScorecardPoints reads the nodeperformance.scorecards.recomputed data points as
// "outcome/reason" -> value, checking the instrument's unit and description.
func collectScorecardPoints(t *testing.T, reader sdkmetric.Reader) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "nodeperformance.scorecards.recomputed" {
				addPoints(t, m, got)
			}
		}
	}
	return got
}

func addPoints(t *testing.T, m metricdata.Metrics, got map[string]int64) {
	t.Helper()
	if m.Unit != "{scorecard}" || m.Description == "" {
		t.Errorf("unit = %q, description empty = %v; the convention wants a UCUM unit and a description", m.Unit, m.Description == "")
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("nodeperformance.scorecards.recomputed is a %T, want an int64 Sum", m.Data)
	}
	for _, point := range sum.DataPoints {
		outcome, _ := point.Attributes.Value("outcome")
		reason, _ := point.Attributes.Value("reason")
		got[outcome.AsString()+"/"+reason.AsString()] = point.Value
	}
}

// Whatever the global provider is, recording is fire-and-forget.
func TestScorecardMetricsRecordingNeverPanics(t *testing.T) {
	metrics, err := telemetry.NewScorecardMetrics()
	if err != nil {
		t.Fatal(err)
	}
	metrics.ScorecardRecomputed(context.Background())
	metrics.ScorecardRecomputeFailed(context.Background(), "internal-error")
}
