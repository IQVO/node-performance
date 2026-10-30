package usecases_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/usecases"
)

// recordingMetrics counts the ports.ScorecardMetrics calls of a use case.
type recordingMetrics struct {
	mu         sync.Mutex
	recomputed int
	failed     []string
}

var _ ports.ScorecardMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) ScorecardRecomputed(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recomputed++
}

func (m *recordingMetrics) ScorecardRecomputeFailed(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failed = append(m.failed, reason)
}

func TestRecomputeCountsEachScorecardOnceByOutcome(t *testing.T) {
	m := &recordingMetrics{}
	e := newEnv(func(d *usecases.Deps) { d.Metrics = m })
	e.feedN(t, "node-1", 1)

	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.recomputed != 2 || len(m.failed) != 0 {
		t.Fatalf("after a run: recomputed=%d failed=%v, want 2 / none (the 7d and the 30d scorecard)", m.recomputed, m.failed)
	}

	// Nothing is due any more: an idle run is not an attempt.
	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.recomputed != 2 || len(m.failed) != 0 {
		t.Fatalf("after an idle run: recomputed=%d failed=%v, want unchanged", m.recomputed, m.failed)
	}
}

func TestRecomputeCountsAFailureWithItsReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"a lost race", ports.ErrConcurrentUpdate, "concurrent-update"},
		{"a database error", errors.New("db down"), "internal-error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &recordingMetrics{}
			e := newEnv(func(d *usecases.Deps) { d.Metrics = m })
			e.feedN(t, "node-1", 1)
			deps := e.deps
			deps.Scorecards = &failingScorecards{ScorecardRepository: e.deps.Scorecards, err: tc.err, n: 1000}
			_, _ = usecases.NewRecompute(deps).Run(context.Background())

			if m.recomputed != 0 || len(m.failed) == 0 || m.failed[0] != tc.want {
				t.Fatalf("recomputed=%d failed=%v, want 0 / %q first", m.recomputed, m.failed, tc.want)
			}
		})
	}
}

// A Deps without Metrics records nothing and never panics.
func TestRecomputeWithoutMetricsIsANoOp(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
}
