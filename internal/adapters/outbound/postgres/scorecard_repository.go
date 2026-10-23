package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// ScorecardRepo is the Postgres ports.ScorecardRepository: one scorecards row
// plus its scorecard_observations rows.
type ScorecardRepo struct{ pool *pgxpool.Pool }

var _ ports.ScorecardRepository = (*ScorecardRepo)(nil)

// NewScorecardRepo constructs a ScorecardRepo over pool.
func NewScorecardRepo(pool *pgxpool.Pool) *ScorecardRepo { return &ScorecardRepo{pool: pool} }

const scorecardColumns = `node_id, scorecard_window, state, COALESCE(grade, ''),
	on_time_handover_rate, late_ack_rate, reject_rate, expiry_rate, promise_kept_rate,
	feed_freshness_p95_minutes, observations, computed_at, dirty, version`

// scanSnapshot reads one scorecards row (scorecardColumns) into a snapshot
// without observations.
func scanSnapshot(row pgx.Row) (scorecard.Snapshot, error) {
	var (
		s                   scorecard.Snapshot
		node, window, state string
		grade               string
		computed            *time.Time
	)
	err := row.Scan(&node, &window, &state, &grade,
		&s.Metrics.OnTimeHandoverRate, &s.Metrics.LateAckRate, &s.Metrics.RejectRate, &s.Metrics.ExpiryRate,
		&s.Metrics.PromiseKeptRate, &s.Metrics.FeedFreshnessP95Minutes, &s.Metrics.Observations,
		&computed, &s.Dirty, &s.Version)
	if err != nil {
		return scorecard.Snapshot{}, err
	}
	s.NodeID, s.Window, s.State, s.Grade = scorecard.NodeID(node), scorecard.Window(window), scorecard.State(state), scorecard.Grade(grade)
	if computed != nil {
		s.ComputedAt = shared.Instant(*computed)
	}
	return s, nil
}

// Get implements ports.ScorecardRepository.
func (r *ScorecardRepo) Get(ctx context.Context, id string) (*scorecard.NodeScorecard, error) {
	q := queryFor(ctx, r.pool)
	snap, err := scanSnapshot(q.QueryRow(ctx, `SELECT `+scorecardColumns+` FROM scorecards WHERE scorecard_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ports.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read scorecard %s: %w", id, err)
	}
	rows, err := q.Query(ctx, `SELECT source_event_id, kind, observed_at, late, on_time
		FROM scorecard_observations WHERE scorecard_id = $1
		ORDER BY observed_at, source_event_id COLLATE "C"`, id)
	if err != nil {
		return nil, fmt.Errorf("read observations of %s: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			o    scorecard.Observation
			kind string
		)
		if err := rows.Scan(&o.SourceEventID, &kind, &o.At, &o.Late, &o.OnTime); err != nil {
			return nil, fmt.Errorf("scan observation of %s: %w", id, err)
		}
		o.Kind = scorecard.Kind(kind)
		snap.Observations = append(snap.Observations, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read observations of %s: %w", id, err)
	}
	return scorecard.Restore(snap)
}

const insertScorecard = `INSERT INTO scorecards (scorecard_id, node_id, scorecard_window, state, grade,
	on_time_handover_rate, late_ack_rate, reject_rate, expiry_rate, promise_kept_rate,
	feed_freshness_p95_minutes, observations, computed_at, dirty, expires_at, version)
	VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`

const updateScorecard = `UPDATE scorecards SET node_id = $2, scorecard_window = $3, state = $4, grade = NULLIF($5, ''),
	on_time_handover_rate = $6, late_ack_rate = $7, reject_rate = $8, expiry_rate = $9, promise_kept_rate = $10,
	feed_freshness_p95_minutes = $11, observations = $12, computed_at = $13, dirty = $14, expires_at = $15, version = $16
	WHERE scorecard_id = $1 AND version = $17`

// Save implements ports.ScorecardRepository: the row is inserted (version 1)
// or updated under the optimistic version check, and the observation set is
// replaced, in one atomic write.
func (r *ScorecardRepo) Save(ctx context.Context, s *scorecard.NodeScorecard) error {
	snap := s.Snapshot()
	id := s.ID()
	var expires *time.Time
	if at, ok := s.ExpiresAt(); ok {
		expires = &at
	}
	var computed *time.Time
	if !snap.ComputedAt.IsZero() {
		computed = &snap.ComputedAt
	}
	args := []any{id, string(snap.NodeID), string(snap.Window), string(snap.State), string(snap.Grade),
		snap.Metrics.OnTimeHandoverRate, snap.Metrics.LateAckRate, snap.Metrics.RejectRate, snap.Metrics.ExpiryRate,
		snap.Metrics.PromiseKeptRate, snap.Metrics.FeedFreshnessP95Minutes, snap.Metrics.Observations,
		computed, snap.Dirty, expires, snap.Version}
	return atomically(ctx, r.pool, func(tx pgx.Tx) error {
		if snap.Version == 1 {
			if _, err := tx.Exec(ctx, insertScorecard, args...); err != nil {
				return translateCreate(err, "scorecard "+id)
			}
		} else {
			tag, err := tx.Exec(ctx, updateScorecard, append(args, snap.Version-1)...)
			if err != nil {
				return fmt.Errorf("update scorecard %s: %w", id, err)
			}
			if tag.RowsAffected() != 1 {
				return errStaleVersion("scorecard "+id, snap.Version-1)
			}
		}
		return replaceObservations(ctx, tx, id, snap.Observations)
	})
}

// replaceObservations makes the stored set equal to list: rows that are not in
// it are deleted, new ones inserted. Observations never change once stored, so
// an existing row is left alone.
func replaceObservations(ctx context.Context, tx pgx.Tx, id string, list []scorecard.Observation) error {
	ids := make([]string, len(list))
	kinds := make([]string, len(list))
	at := make([]time.Time, len(list))
	late := make([]bool, len(list))
	onTime := make([]bool, len(list))
	for i, o := range list {
		ids[i], kinds[i], at[i], late[i], onTime[i] = o.SourceEventID, string(o.Kind), o.At, o.Late, o.OnTime
	}
	if _, err := tx.Exec(ctx, `DELETE FROM scorecard_observations
		WHERE scorecard_id = $1 AND source_event_id <> ALL($2::text[])`, id, ids); err != nil {
		return fmt.Errorf("drop observations of %s: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scorecard_observations (scorecard_id, source_event_id, kind, observed_at, late, on_time)
		SELECT $1, t.id, t.kind, t.at, t.late, t.on_time
		FROM unnest($2::text[], $3::text[], $4::timestamptz[], $5::boolean[], $6::boolean[]) AS t(id, kind, at, late, on_time)
		ON CONFLICT (scorecard_id, source_event_id) DO NOTHING`, id, ids, kinds, at, late, onTime); err != nil {
		return fmt.Errorf("store observations of %s: %w", id, err)
	}
	return nil
}

// ListDue implements ports.ScorecardRepository.
func (r *ScorecardRepo) ListDue(ctx context.Context, now time.Time, limit int) ([]string, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT scorecard_id FROM scorecards
		WHERE dirty OR expires_at <= $1
		ORDER BY scorecard_id COLLATE "C" LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due scorecards: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan due scorecard: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
