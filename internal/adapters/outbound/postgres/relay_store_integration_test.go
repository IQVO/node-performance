//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/application/outbox"
)

// seedOutbox appends the three events of repocontract.RaisedEvents n times
// and returns the store plus the ids of its rows in order.
func seedOutbox(t *testing.T, times int) *postgres.RelayStore {
	t.Helper()
	f, pool := newFixture(t)
	for i := 0; i < times; i++ {
		noErr(t, f.Outbox.Append(context.Background(), repocontract.RaisedEvents(t)...))
	}
	return postgres.NewRelayStore(pool)
}

func published(t *testing.T, store *postgres.RelayStore, limit, maxAttempts int, send func(context.Context, outbox.Message) error) (int, error) {
	t.Helper()
	return store.Drain(context.Background(), limit, maxAttempts, send)
}

func TestDrainSendsInIdOrderAndMarksPublished(t *testing.T) {
	store := seedOutbox(t, 1)
	var order []string
	n, err := published(t, store, 10, 5, func(_ context.Context, m outbox.Message) error {
		order = append(order, m.EventID)
		if len(m.Headers) != 1 || m.Headers[0].Key != "content-type" || m.Subject == "" || len(m.Value) == 0 || len(m.Key) == 0 {
			t.Errorf("incomplete message: %+v", m)
		}
		return nil
	})
	if err != nil || n != 3 || len(order) != 3 {
		t.Fatalf("Drain = %d, %v, %v", n, err, order)
	}
	if again, err := published(t, store, 10, 5, func(context.Context, outbox.Message) error { t.Fatal("nothing is left"); return nil }); err != nil || again != 0 {
		t.Fatalf("second Drain = %d, %v", again, err)
	}
}

func TestDrainHonoursTheBatchLimit(t *testing.T) {
	store := seedOutbox(t, 1)
	n, err := published(t, store, 2, 5, func(context.Context, outbox.Message) error { return nil })
	if err != nil || n != 2 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
	n, err = published(t, store, 2, 5, func(context.Context, outbox.Message) error { return nil })
	if err != nil || n != 1 {
		t.Fatalf("Drain = %d, %v", n, err)
	}
}

func TestAFailedSendStopsThePassKeepsTheSentRowsAndRecordsTheFailure(t *testing.T) {
	store := seedOutbox(t, 1)
	boom := errors.New("broker down")
	calls := 0
	n, err := published(t, store, 10, 5, func(context.Context, outbox.Message) error {
		calls++
		if calls == 2 {
			return boom
		}
		return nil
	})
	var sf *outbox.SendFailure
	if !errors.As(err, &sf) || !errors.Is(err, boom) || sf.Attempts != 1 || n != 1 || calls != 2 {
		t.Fatalf("Drain = %d, %v (calls %d)", n, err, calls)
	}
	// the row that failed is retried first, the one already sent is not sent again
	var resent []string
	n, err = published(t, store, 10, 5, func(_ context.Context, m outbox.Message) error { resent = append(resent, m.EventID); return nil })
	if err != nil || n != 2 || len(resent) != 2 {
		t.Fatalf("retry = %d, %v", n, err)
	}
}

func TestTheAttemptCapExcludesAPoisonRow(t *testing.T) {
	store := seedOutbox(t, 1)
	poison := errors.New("poison")
	first := ""
	send := func(_ context.Context, m outbox.Message) error {
		if first == "" {
			first = m.EventID
		}
		if m.EventID == first {
			return poison
		}
		return nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		n, err := published(t, store, 10, 3, send)
		var sf *outbox.SendFailure
		if !errors.As(err, &sf) || sf.Attempts != attempt || n != 0 {
			t.Fatalf("attempt %d: %d, %v", attempt, n, err)
		}
	}
	// the cap is reached: the poison row is no longer claimed, the rows behind it flow
	n, err := published(t, store, 10, 3, send)
	if err != nil || n != 2 {
		t.Fatalf("after the cap: %d, %v", n, err)
	}
	// with no cap (0) the row is claimed again
	n, err = published(t, store, 10, 0, send)
	var sf *outbox.SendFailure
	if !errors.As(err, &sf) || sf.Attempts != 4 || n != 0 {
		t.Fatalf("unlimited: %d, %v", n, err)
	}
}

func TestOverlappingDrainsNeverClaimTheSameRow(t *testing.T) {
	store := seedOutbox(t, 4) // 12 rows
	started := make(chan struct{})
	release := make(chan struct{})
	seenA := map[string]bool{}
	done := make(chan error, 1)
	go func() {
		_, err := published(t, store, 6, 5, func(_ context.Context, m outbox.Message) error {
			if len(seenA) == 0 {
				close(started)
				<-release
			}
			seenA[m.EventID] = true
			return nil
		})
		done <- err
	}()
	<-started
	seenB := map[string]bool{}
	nB, err := published(t, store, 12, 5, func(_ context.Context, m outbox.Message) error { seenB[m.EventID] = true; return nil })
	noErr(t, err)
	close(release)
	noErr(t, <-done)
	for id := range seenA {
		if seenB[id] {
			t.Fatalf("row %s was claimed by both passes", id)
		}
	}
	if nB+len(seenA) != 12 {
		t.Fatalf("passes published %d + %d rows, want 12 in all", nB, len(seenA))
	}
}
