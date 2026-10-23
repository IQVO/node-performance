//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/node-performance/internal/adapters/outbound/postgres"
	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// execErr runs sql on a fresh database and returns the error.
func execErr(t *testing.T, sql string, args ...any) error {
	t.Helper()
	pool := newPool(t)
	_, err := pool.Exec(context.Background(), sql, args...)
	return err
}

const validScorecard = `INSERT INTO scorecards (scorecard_id, node_id, scorecard_window, state, grade, on_time_handover_rate,
	late_ack_rate, reject_rate, expiry_rate, promise_kept_rate, feed_freshness_p95_minutes, observations, dirty, version)
	VALUES ($1, $2, $3, $4, $5, $6, 0, 0, 0, 1, 0, $7, false, $8)`

func TestTheSchemaRefusesWhatBreaksAnInvariant(t *testing.T) {
	for name, tc := range map[string]struct {
		args []any
		want bool // true: the insert must succeed
	}{
		"a valid provisional row":      {[]any{"n:7d", "n", "7d", "PROVISIONAL", nil, 1.0, 3, 1}, true},
		"a valid graded row":           {[]any{"n:30d", "n", "30d", "GRADED", "B", 0.9, 50, 2}, true},
		"a rate above 1":               {[]any{"n:7d", "n", "7d", "PROVISIONAL", nil, 1.5, 3, 1}, false},
		"a negative rate":              {[]any{"n:7d", "n", "7d", "PROVISIONAL", nil, -0.1, 3, 1}, false},
		"an unknown window":            {[]any{"n:1d", "n", "1d", "PROVISIONAL", nil, 1.0, 3, 1}, false},
		"an id that is not node:win":   {[]any{"x:7d", "n", "7d", "PROVISIONAL", nil, 1.0, 3, 1}, false},
		"graded without a grade":       {[]any{"n:7d", "n", "7d", "GRADED", nil, 1.0, 50, 1}, false},
		"provisional with a grade":     {[]any{"n:7d", "n", "7d", "PROVISIONAL", "A", 1.0, 3, 1}, false},
		"graded below the minimum":     {[]any{"n:7d", "n", "7d", "GRADED", "A", 1.0, 49, 1}, false},
		"an unknown grade":             {[]any{"n:7d", "n", "7d", "GRADED", "E", 1.0, 50, 1}, false},
		"a negative observation count": {[]any{"n:7d", "n", "7d", "PROVISIONAL", nil, 1.0, -1, 1}, false},
		"version zero":                 {[]any{"n:7d", "n", "7d", "PROVISIONAL", nil, 1.0, 3, 0}, false},
		"an unknown state":             {[]any{"n:7d", "n", "7d", "DONE", nil, 1.0, 3, 1}, false},
	} {
		err := execErr(t, validScorecard, tc.args...)
		if (err == nil) != tc.want {
			t.Errorf("%s: err = %v, want success %v", name, err, tc.want)
		}
	}
}

func TestTheGradeLabelIsGeneratedFromStateAndGrade(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	noErr(t, func() error {
		_, err := pool.Exec(ctx, validScorecard, "n:7d", "n", "7d", "PROVISIONAL", nil, 1.0, 3, 1)
		return err
	}())
	noErr(t, func() error {
		_, err := pool.Exec(ctx, validScorecard, "n:30d", "n", "30d", "GRADED", "C", 0.9, 60, 1)
		return err
	}())
	rows, err := pool.Query(ctx, `SELECT scorecard_id, grade_label FROM scorecards ORDER BY scorecard_id COLLATE "C"`)
	noErr(t, err)
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, label string
		noErr(t, rows.Scan(&id, &label))
		got[id] = label
	}
	if got["n:7d"] != "PROVISIONAL" || got["n:30d"] != "C" {
		t.Fatalf("grade labels = %v", got)
	}
}

func TestObservationsFollowTheirScorecard(t *testing.T) {
	f, pool := newFixture(t)
	repocontract.Seed(t, f, "node-1", scorecard.Window7d, 3)
	if n := count(t, pool, "scorecard_observations"); n != 3 {
		t.Fatalf("observations = %d", n)
	}
	var pgErr *pgconn.PgError
	_, err := pool.Exec(context.Background(), `INSERT INTO scorecard_observations (scorecard_id, source_event_id, kind, observed_at)
		VALUES ('ghost:7d', 'e', 'HANDOVER', now())`)
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("an orphan observation must violate the foreign key: %v", err)
	}
	_, err = pool.Exec(context.Background(), `INSERT INTO scorecard_observations (scorecard_id, source_event_id, kind, observed_at)
		VALUES ('node-1:7d', 'e', 'NOPE', now())`)
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatalf("an unknown kind must violate its check: %v", err)
	}
	_, err = pool.Exec(context.Background(), `DELETE FROM scorecards WHERE scorecard_id = 'node-1:7d'`)
	noErr(t, err)
	if n := count(t, pool, "scorecard_observations"); n != 0 {
		t.Fatalf("observations must cascade: %d", n)
	}
}

func TestOnlyOneWriterCreatesAScorecard(t *testing.T) {
	f, pool := newFixture(t)
	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.Scorecards.Save(context.Background(), repocontract.NewScorecard(t, "node-1", scorecard.Window7d))
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ports.ErrConcurrentUpdate):
			t.Fatalf("a losing create must be ErrConcurrentUpdate: %v", err)
		}
	}
	if wins != 1 || count(t, pool, "scorecards") != 1 {
		t.Fatalf("winners = %d, rows = %d", wins, count(t, pool, "scorecards"))
	}
}

func TestOnlyOneWriterWinsAVersion(t *testing.T) {
	f, pool := newFixture(t)
	repocontract.Seed(t, f, "node-1", scorecard.Window7d, 1)
	const writers = 8
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range errs {
		loaded, err := f.Scorecards.Get(context.Background(), "node-1:7d")
		noErr(t, err)
		noErr(t, loaded.RecordObservation(repocontract.Feed(t, "feed-"+string(rune('a'+i)), 10+i)))
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f.Scorecards.Save(context.Background(), loaded)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, ports.ErrConcurrentUpdate):
			t.Fatalf("a losing update must be ErrConcurrentUpdate: %v", err)
		}
	}
	var version int64
	noErr(t, pool.QueryRow(context.Background(), `SELECT version FROM scorecards WHERE scorecard_id = 'node-1:7d'`).Scan(&version))
	if wins != 1 || version != 2 || count(t, pool, "scorecard_observations") != 2 {
		t.Fatalf("winners = %d, version = %d, observations = %d", wins, version, count(t, pool, "scorecard_observations"))
	}
}

func TestConcurrentInboxClaimsOfOneIdHaveOneWinner(t *testing.T) {
	f, pool := newFixture(t)
	const claimers = 8
	fresh := make([]bool, claimers)
	var wg sync.WaitGroup
	for i := range fresh {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := f.Inbox.MarkProcessed(context.Background(), "evt-1", "com.example.A", repocontract.Now)
			noErr(t, err)
			fresh[i] = ok
		}()
	}
	wg.Wait()
	wins := 0
	for _, ok := range fresh {
		if ok {
			wins++
		}
	}
	if wins != 1 || count(t, pool, "processed_events") != 1 {
		t.Fatalf("winners = %d, rows = %d", wins, count(t, pool, "processed_events"))
	}
}

func TestAFailedWriteInsideAUnitOfWorkDoesNotPoisonTheUnit(t *testing.T) {
	f, pool := newFixture(t)
	repocontract.Seed(t, f, "node-1", scorecard.Window7d, 1)
	err := f.UoW.Do(context.Background(), func(ctx context.Context) error {
		dup := f.Scorecards.Save(ctx, repocontract.NewScorecard(t, "node-1", scorecard.Window7d))
		if !errors.Is(dup, ports.ErrConcurrentUpdate) {
			t.Fatalf("duplicate create = %v", dup)
		}
		// the unit's transaction is still usable: a unique violation inside a savepoint
		return f.Scorecards.Save(ctx, repocontract.NewScorecard(t, "node-2", scorecard.Window7d))
	})
	noErr(t, err)
	if n := count(t, pool, "scorecards"); n != 2 {
		t.Fatalf("scorecards = %d, want 2", n)
	}
}

func TestTheOutboxStoresTheEncodedMessage(t *testing.T) {
	f, pool := newFixture(t)
	events := repocontract.RaisedEvents(t)
	noErr(t, f.Outbox.Append(context.Background(), events...))
	var (
		eventType, topic, subject, dataschema, headers string
		key, value                                     []byte
		attempts                                       int
		published                                      *string
	)
	noErr(t, pool.QueryRow(context.Background(), `SELECT event_type, topic, subject, dataschema, headers::text, key, value, attempts, published_at::text
		FROM outbox_events ORDER BY id LIMIT 1`).Scan(&eventType, &topic, &subject, &dataschema, &headers, &key, &value, &attempts, &published))
	if eventType != "com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated" || topic != eventsTopic ||
		subject != "node-9:7d" || string(key) != "node-9:7d" || attempts != 0 || published != nil {
		t.Fatalf("row = %s %s %s %s attempts %d published %v", eventType, topic, subject, key, attempts, published)
	}
	if dataschema != "urn:retailnetwork:node-performance:events:NodeScorecardUpdated:v1" {
		t.Fatalf("dataschema = %s", dataschema)
	}
	var doc []map[string]string
	noErr(t, json.Unmarshal([]byte(headers), &doc))
	if len(doc) != 1 || doc[0]["key"] != "content-type" {
		t.Fatalf("headers = %s", headers)
	}
	var ce map[string]any
	noErr(t, json.Unmarshal(value, &ce))
	if ce["specversion"] != "1.0" || ce["subject"] != "node-9:7d" {
		t.Fatalf("value = %s", value)
	}
	if n := count(t, pool, "outbox_events"); n != len(events) {
		t.Fatalf("rows = %d, want %d", n, len(events))
	}
}

func TestTheOutboxEncodesBeforeItWrites(t *testing.T) {
	_, pool := newFixture(t)
	events := repocontract.RaisedEvents(t)
	failing := postgres.NewOutboxRepo(pool, testEncoder{failOn: "NodeThresholdBreached"})
	if err := failing.Append(context.Background(), events...); !errors.Is(err, errEncode) {
		t.Fatalf("err = %v", err)
	}
	if n := count(t, pool, "outbox_events"); n != 0 {
		t.Fatalf("an encoding failure must write nothing: %d rows", n)
	}
	if err := postgres.NewOutboxRepo(pool, nil).Append(context.Background(), events...); !errors.Is(err, postgres.ErrNilEncoder) {
		t.Fatalf("err = %v", err)
	}
	noErr(t, postgres.NewOutboxRepo(pool, nil).Append(context.Background()))
}

func TestNewPoolRefusesABadURL(t *testing.T) {
	if _, err := postgres.NewPool(context.Background(), "::not a url::"); err == nil {
		t.Fatal("a malformed DSN must fail")
	}
}
