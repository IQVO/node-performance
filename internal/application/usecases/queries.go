package usecases

import (
	"context"
	"fmt"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

const (
	// DefaultListLimit is the page size of ListScorecardsByGrade when none is given.
	DefaultListLimit = 100
	// MaxListLimit is the largest page.
	MaxListLimit = 500
	// maxCursor is the longest accepted `after` (a scorecardId is at most 68).
	maxCursor = 80
)

// Queries are the two read use cases of apis/openapi.yaml.
type Queries struct {
	Reader ports.ScorecardReader
	// Inbound and Clock add the informational inbound metrics to the metrics
	// of each scorecard (ADR 0007). A nil Inbound leaves them out.
	Inbound ports.InboundReader
	Clock   ports.Clock
}

// ListByGradeInput is the validated-on-arrival input of ListScorecardsByGrade.
type ListByGradeInput struct {
	// Grade is A, B, C, D or PROVISIONAL.
	Grade string
	// Window is 7d, 30d or empty for both.
	Window string
	// Limit is the page size; 0 means DefaultListLimit.
	Limit int
	// After is the NextCursor of the previous page.
	After string
}

// GetScorecard returns both scorecards of a node (GetScorecard). A node nothing
// was observed about is ports.ErrNotFound; an id that cannot be a node id is a
// validation error.
func (q Queries) GetScorecard(ctx context.Context, nodeID string) (views.NodeScorecards, error) {
	id, err := scorecard.NewNodeID(nodeID)
	if err != nil {
		return views.NodeScorecards{}, err
	}
	list, err := q.Reader.ByNode(ctx, id)
	if err != nil {
		return views.NodeScorecards{}, err
	}
	if len(list) == 0 {
		return views.NodeScorecards{}, fmt.Errorf("%w: no scorecard for node %s", ports.ErrNotFound, nodeID)
	}
	if err := q.addInbound(ctx, list); err != nil {
		return views.NodeScorecards{}, err
	}
	return views.NodeScorecards{NodeID: string(id), Scorecards: list}, nil
}

// ListScorecardsByGrade returns one page of the scorecards of a grade.
func (q Queries) ListScorecardsByGrade(ctx context.Context, in ListByGradeInput) (views.ScorecardPage, error) {
	filter, err := listFilter(in)
	if err != nil {
		return views.ScorecardPage{}, err
	}
	page, err := q.Reader.ByGrade(ctx, filter)
	if err != nil {
		return views.ScorecardPage{}, err
	}
	if err := q.addInbound(ctx, page.Items); err != nil {
		return views.ScorecardPage{}, err
	}
	return page, nil
}

// addInbound fills the two inbound metrics of every scorecard in place, from
// the receipts joined to their shipments inside each scorecard's own window.
func (q Queries) addInbound(ctx context.Context, items []views.Scorecard) error {
	if q.Inbound == nil || len(items) == 0 {
		return nil
	}
	now := q.Clock.Now().UTC()
	seen := map[scorecard.NodeID]bool{}
	var nodes []scorecard.NodeID
	for _, it := range items {
		if n := scorecard.NodeID(it.NodeID); !seen[n] {
			seen[n] = true
			nodes = append(nodes, n)
		}
	}
	// The longest window bounds the read; each scorecard then applies its own.
	byNode, err := q.Inbound.Observations(ctx, nodes, now.Add(-scorecard.Window30d.Duration()))
	if err != nil {
		return fmt.Errorf("read inbound observations: %w", err)
	}
	for i := range items {
		m := scorecard.ComputeInbound(byNode[scorecard.NodeID(items[i].NodeID)], now, scorecard.Window(items[i].Window))
		items[i].Metrics.InboundTimelinessP95Minutes = m.TimelinessP95Minutes
		items[i].Metrics.InboundAccuracyRate = m.AccuracyRate
	}
	return nil
}

func listFilter(in ListByGradeInput) (repository.ScorecardFilter, error) {
	switch in.Grade {
	case "A", "B", "C", "D", string(scorecard.StateProvisional):
	default:
		return repository.ScorecardFilter{}, shared.Invalid("grade must be one of A, B, C, D, PROVISIONAL")
	}
	f := repository.ScorecardFilter{Grade: in.Grade, Limit: in.Limit, After: in.After}
	if in.Window != "" {
		w, err := scorecard.ParseWindow(in.Window)
		if err != nil {
			return repository.ScorecardFilter{}, err
		}
		f.Window = w
	}
	if f.Limit == 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit < 1 || f.Limit > MaxListLimit {
		return repository.ScorecardFilter{}, shared.Invalid("limit must be between 1 and %d", MaxListLimit)
	}
	if f.After != "" && !shared.ValidTextN(f.After, maxCursor) {
		return repository.ScorecardFilter{}, shared.Invalid("after is not a cursor this service issued")
	}
	return f, nil
}
