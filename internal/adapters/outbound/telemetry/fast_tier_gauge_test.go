package telemetry_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/node-performance/internal/adapters/outbound/telemetry"
)

// gaugeReader installs a manual reader as the global MeterProvider.
func gaugeReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })
	return reader
}

// collectGauge returns the data points of the fast-tier gauge and its metadata.
func collectGauge(t *testing.T, reader sdkmetric.Reader) (points []int64, unit string) {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != "nodeperf.fast_tier.ineligible_nodes" {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			if !ok {
				t.Fatalf("data is %T, want an int64 gauge", m.Data)
			}
			for _, p := range gauge.DataPoints {
				points = append(points, p.Value)
			}
			unit = m.Unit
		}
	}
	return points, unit
}

func TestFastTierGaugeReportsTheCountAtEachCollection(t *testing.T) {
	reader := gaugeReader(t)
	suspended := 3
	if err := telemetry.RegisterFastTierGauge(func(context.Context) (int, error) { return suspended, nil }); err != nil {
		t.Fatalf("RegisterFastTierGauge: %v", err)
	}
	points, unit := collectGauge(t, reader)
	if len(points) != 1 || points[0] != 3 || unit != "{node}" {
		t.Fatalf("points %v unit %q, want [3] {node}", points, unit)
	}
	suspended = 0
	if points, _ = collectGauge(t, reader); len(points) != 1 || points[0] != 0 {
		t.Fatalf("after the reinstatements: %v, want [0]", points)
	}
}

func TestFastTierGaugeSkipsACollectionWhenTheCountFails(t *testing.T) {
	reader := gaugeReader(t)
	if err := telemetry.RegisterFastTierGauge(func(context.Context) (int, error) { return 0, errors.New("database down") }); err != nil {
		t.Fatalf("RegisterFastTierGauge: %v", err)
	}
	if points, _ := collectGauge(t, reader); len(points) != 0 {
		t.Fatalf("a failing count must not report a zero: %v", points)
	}
}
