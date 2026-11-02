//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
)

func TestTheRecomputeLeaseIsExclusiveAndReleasable(t *testing.T) {
	pool := newPool(t)
	first := postgres.NewRecomputeLease(pool)
	second := postgres.NewRecomputeLease(pool)
	ctx := context.Background()

	release, ok, err := first.Acquire(ctx)
	if err != nil || !ok {
		t.Fatalf("first acquire: ok=%v err=%v", ok, err)
	}
	if _, ok, err := second.Acquire(ctx); err != nil || ok {
		t.Fatalf("a second acquire must see the lease held: ok=%v err=%v", ok, err)
	}
	release()

	release2, ok, err := second.Acquire(ctx)
	if err != nil || !ok {
		t.Fatalf("acquire after release: ok=%v err=%v", ok, err)
	}
	release2()
}
