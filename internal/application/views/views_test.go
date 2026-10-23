package views_test

import (
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

func TestScorecardOfMapsEveryField(t *testing.T) {
	at := time.Date(2026, 10, 22, 14, 3, 0, 0, time.UTC)
	m := scorecard.Metrics{
		OnTimeHandoverRate: 0.962, LateAckRate: 0.021, RejectRate: 0.012, ExpiryRate: 0.004,
		PromiseKeptRate: 0.951, FeedFreshnessP95Minutes: 38, Observations: 1874,
	}
	got := views.ScorecardOf(scorecard.Snapshot{
		NodeID: "N-0042", Window: scorecard.Window30d, State: scorecard.StateGraded, Grade: scorecard.GradeB,
		Metrics: m, ComputedAt: at, Version: 3,
	})
	want := views.Scorecard{
		ScorecardID: "N-0042:30d", NodeID: "N-0042", Window: "30d", State: "GRADED", Grade: "B",
		Metrics: views.Metrics{
			OnTimeHandoverRate: 0.962, LateAckRate: 0.021, RejectRate: 0.012, ExpiryRate: 0.004,
			PromiseKeptRate: 0.951, FeedFreshnessP95Minutes: 38, Observations: 1874,
		},
		ComputedAt: at,
	}
	if got != want {
		t.Fatalf("view = %+v, want %+v", got, want)
	}
}

func TestAProvisionalScorecardReadsAsProvisional(t *testing.T) {
	got := views.ScorecardOf(scorecard.Snapshot{NodeID: "n", Window: scorecard.Window7d, State: scorecard.StateProvisional, Metrics: scorecard.EmptyMetrics(), Version: 1})
	if got.Grade != "PROVISIONAL" || got.State != "PROVISIONAL" || !got.ComputedAt.IsZero() || got.Metrics.OnTimeHandoverRate != 1 {
		t.Fatalf("view = %+v", got)
	}
}
