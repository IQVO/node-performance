package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/node-performance/internal/application/outbox"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// The tests in this file are the container-free unit tests of the package:
// pure helpers and the code paths that return before any statement reaches a
// pool. The SQL paths live in the build-tagged integration tests.

func TestInstant(t *testing.T) {
	zero := time.Time{}
	if got := instant(nil); !got.IsZero() {
		t.Fatalf("instant(nil) = %v, want the zero time", got)
	}
	if got := instant(&zero); !got.IsZero() {
		t.Fatalf("instant(zero) = %v, want the zero time", got)
	}
	withNanos := time.Date(2026, 10, 23, 12, 0, 0, 999_999_999, time.FixedZone("BRT", -3*3600))
	want := time.Date(2026, 10, 23, 15, 0, 0, 999999000, time.UTC)
	if got := instant(&withNanos); !got.Equal(want) {
		t.Fatalf("instant(%v) = %v, want %v (UTC, microsecond)", withNanos, got, want)
	}
}

func TestNullable(t *testing.T) {
	if got := nullable(time.Time{}); got != nil {
		t.Fatalf("nullable(zero) = %v, want nil", got)
	}
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	got := nullable(at)
	if got == nil || !got.Equal(at) {
		t.Fatalf("nullable(%v) = %v, want a pointer to it", at, got)
	}
}

func TestMarshalHeaders(t *testing.T) {
	cases := []struct {
		name string
		in   []outbox.Header
		want string
	}{
		{"none", nil, "[]"},
		{"one", []outbox.Header{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}},
			`[{"key":"content-type","value":"application/cloudevents+json; charset=UTF-8"}]`},
		{"two", []outbox.Header{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}},
			`[{"key":"a","value":"1"},{"key":"b","value":"2"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := marshalHeaders(tc.in)
			if err != nil {
				t.Fatalf("marshalHeaders: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("marshalHeaders = %s, want %s", b, tc.want)
			}
		})
	}
}

func TestUnmarshalHeadersRoundTrip(t *testing.T) {
	for _, in := range [][]outbox.Header{
		{},
		{{Key: "content-type", Value: "application/cloudevents+json; charset=UTF-8"}},
		{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}},
	} {
		b, err := marshalHeaders(in)
		if err != nil {
			t.Fatalf("marshalHeaders: %v", err)
		}
		back, err := unmarshalHeaders(b)
		if err != nil {
			t.Fatalf("unmarshalHeaders(%s): %v", b, err)
		}
		if len(back) != len(in) {
			t.Fatalf("round trip of %v = %v", in, back)
		}
		for i := range back {
			if back[i] != in[i] {
				t.Fatalf("round trip of %v = %v", in, back)
			}
		}
	}
	if _, err := unmarshalHeaders([]byte("not json")); err == nil {
		t.Fatal("unmarshalHeaders accepted invalid JSON")
	}
}

// fakeRow is a pgx.Row that hands out one column list, or an error.
type fakeRow struct {
	vals []any
	err  error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.vals) {
		return errors.New("fakeRow: column count mismatch")
	}
	for i, v := range r.vals {
		switch d := dest[i].(type) {
		case *string:
			*d = v.(string)
		case *float64:
			*d = v.(float64)
		case *int:
			*d = v.(int)
		case *int64:
			*d = v.(int64)
		case *bool:
			*d = v.(bool)
		case *time.Time:
			*d = v.(time.Time)
		case **time.Time:
			if v == nil {
				*d = nil
			} else {
				at := v.(time.Time)
				*d = &at
			}
		default:
			return errors.New("fakeRow: unsupported destination")
		}
	}
	return nil
}

// gradedColumns is one GRADED scorecards row in scorecardColumns order; a
// nil computed means the scorecard was never recomputed.
func gradedColumns(computed *time.Time) []any {
	var at any
	if computed != nil {
		at = *computed
	}
	return []any{"N-1", "30d", "GRADED", "A",
		0.98, 0.01, 0.0, 0.0, 0.97, 240, 512,
		at, true, int64(7)}
}

func TestScanSnapshotGraded(t *testing.T) {
	at := time.Date(2026, 10, 23, 12, 0, 0, 999, time.UTC)
	snap, err := scanSnapshot(fakeRow{vals: gradedColumns(&at)})
	if err != nil {
		t.Fatalf("scanSnapshot: %v", err)
	}
	if snap.NodeID != scorecard.NodeID("N-1") || snap.Window != scorecard.Window30d {
		t.Fatalf("identity = %s:%s, want N-1:30d", snap.NodeID, snap.Window)
	}
	if snap.State != scorecard.StateGraded || snap.Grade != scorecard.GradeA {
		t.Fatalf("state/grade = %s/%s, want GRADED/A", snap.State, snap.Grade)
	}
	if !snap.ComputedAt.Equal(shared.Instant(at)) {
		t.Fatalf("computedAt = %v, want %v", snap.ComputedAt, shared.Instant(at))
	}
	if !snap.Dirty || snap.Version != 7 {
		t.Fatalf("dirty/version = %v/%d, want true/7", snap.Dirty, snap.Version)
	}
	wantMetrics := scorecard.Metrics{OnTimeHandoverRate: 0.98, LateAckRate: 0.01, PromiseKeptRate: 0.97, FeedFreshnessP95Minutes: 240, Observations: 512}
	if snap.Metrics != wantMetrics {
		t.Fatalf("metrics = %+v, want %+v", snap.Metrics, wantMetrics)
	}
}

func TestScanSnapshotProvisionalNeverComputed(t *testing.T) {
	snap, err := scanSnapshot(fakeRow{vals: []any{"N-2", "7d", "PROVISIONAL", "",
		1.0, 0.0, 0.0, 0.0, 1.0, 0, 3,
		nil, true, int64(3)}})
	if err != nil {
		t.Fatalf("scanSnapshot: %v", err)
	}
	if snap.State != scorecard.StateProvisional || snap.Grade != "" {
		t.Fatalf("state/grade = %s/%q, want PROVISIONAL/\"\"", snap.State, snap.Grade)
	}
	if !snap.ComputedAt.IsZero() {
		t.Fatalf("computedAt = %v, want the zero time", snap.ComputedAt)
	}
	if snap.Metrics.Observations != 3 {
		t.Fatalf("observations = %d, want 3", snap.Metrics.Observations)
	}
}

func TestScanSnapshotPropagatesTheScanError(t *testing.T) {
	cause := errors.New("connection reset")
	if _, err := scanSnapshot(fakeRow{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("scanSnapshot error = %v, want %v", err, cause)
	}
}

// fakeRows is a pgx.Rows over a fixed column list. Only the methods scanViews
// calls are implemented; the rest of the interface stays embedded.
type fakeRows struct {
	pgx.Rows
	rows []fakeRow
	i    int
	err  error
}

func (r *fakeRows) Next() bool { r.i++; return r.i <= len(r.rows) }

func (r *fakeRows) Close() {}

func (r *fakeRows) Scan(dest ...any) error { return r.rows[r.i-1].Scan(dest...) }

func (r *fakeRows) Err() error { return r.err }

func TestScanViews(t *testing.T) {
	rows := &fakeRows{rows: []fakeRow{
		{vals: gradedColumns(nil)},
		{vals: []any{"N-2", "7d", "PROVISIONAL", "", 1.0, 0.0, 0.0, 0.0, 1.0, 0, 2, nil, false, int64(2)}},
	}}
	got, err := scanViews(rows)
	if err != nil {
		t.Fatalf("scanViews: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("scanViews returned %d views, want 2", len(got))
	}
	if got[0].ScorecardID != "N-1:30d" || got[0].Grade != "A" || got[0].State != "GRADED" {
		t.Fatalf("first view = %+v, want N-1:30d GRADED A", got[0])
	}
	if got[1].ScorecardID != "N-2:7d" || got[1].Grade != "PROVISIONAL" {
		t.Fatalf("second view = %+v, want N-2:7d PROVISIONAL", got[1])
	}
}

func TestScanViewsWrapsAScanError(t *testing.T) {
	cause := errors.New("broken pipe")
	rows := &fakeRows{rows: []fakeRow{{err: cause}}}
	if _, err := scanViews(rows); !errors.Is(err, cause) {
		t.Fatalf("scanViews error = %v, want it to wrap %v", err, cause)
	}
}

func TestScanViewsPropagatesRowsErr(t *testing.T) {
	cause := errors.New("lost connection")
	if _, err := scanViews(&fakeRows{err: cause}); !errors.Is(err, cause) {
		t.Fatalf("scanViews error = %v, want %v", err, cause)
	}
}

// carriedTx stands in for the transaction a unit of work carries in the ctx.
type carriedTx struct{ pgx.Tx }

func TestQueryForPrefersThePoolWithoutATransaction(t *testing.T) {
	var pool *pgxpool.Pool // a nil pool stands in for the caller's pool
	if got := queryFor(context.Background(), pool); got != querier(pool) {
		t.Fatalf("queryFor without a tx = %v, want the pool as given", got)
	}
	if got := queryFor(pgtx.With(context.Background(), nil), pool); got != querier(pool) {
		t.Fatal("a nil transaction must not be carried")
	}
}

func TestQueryForReturnsTheTransactionOfTheContext(t *testing.T) {
	tx := &carriedTx{}
	if got := queryFor(pgtx.With(context.Background(), tx), nil); !sameQuerier(got, tx) {
		t.Fatalf("queryFor = %v, want the carried transaction", got)
	}
}

// sameQuerier reports whether q is exactly tx.
func sameQuerier(q querier, tx pgx.Tx) bool {
	carried, ok := q.(pgx.Tx)
	return ok && carried == tx
}
