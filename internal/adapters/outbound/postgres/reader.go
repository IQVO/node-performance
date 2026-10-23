package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// Reader is the Postgres ports.ScorecardReader: it reads the scorecards rows
// only, never the observations.
type Reader struct{ pool *pgxpool.Pool }

var _ ports.ScorecardReader = (*Reader)(nil)

// NewReader constructs a Reader over pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

func scanViews(rows pgx.Rows) ([]views.Scorecard, error) {
	defer rows.Close()
	out := []views.Scorecard{}
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scan scorecard: %w", err)
		}
		out = append(out, views.ScorecardOf(snap))
	}
	return out, rows.Err()
}

// ByNode implements ports.ScorecardReader.
func (r *Reader) ByNode(ctx context.Context, nodeID scorecard.NodeID) ([]views.Scorecard, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+scorecardColumns+` FROM scorecards
		WHERE node_id = $1
		ORDER BY CASE scorecard_window WHEN '7d' THEN 0 ELSE 1 END`, string(nodeID))
	if err != nil {
		return nil, fmt.Errorf("read scorecards of %s: %w", nodeID, err)
	}
	return scanViews(rows)
}

// ByGrade implements ports.ScorecardReader: one extra row is read to know
// whether another page follows.
func (r *Reader) ByGrade(ctx context.Context, f repository.ScorecardFilter) (views.ScorecardPage, error) {
	rows, err := queryFor(ctx, r.pool).Query(ctx, `SELECT `+scorecardColumns+` FROM scorecards
		WHERE grade_label = $1
		  AND ($2 = '' OR scorecard_window = $2)
		  AND ($3 = '' OR scorecard_id COLLATE "C" > $3 COLLATE "C")
		ORDER BY scorecard_id COLLATE "C" LIMIT $4`, f.Grade, string(f.Window), f.After, f.Limit+1)
	if err != nil {
		return views.ScorecardPage{}, fmt.Errorf("list scorecards by grade: %w", err)
	}
	items, err := scanViews(rows)
	if err != nil {
		return views.ScorecardPage{}, err
	}
	page := views.ScorecardPage{Items: items}
	if len(items) > f.Limit {
		page.Items = items[:f.Limit]
		page.NextCursor = page.Items[f.Limit-1].ScorecardID
	}
	return page, nil
}
