package http

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/node-performance/internal/adapters/inbound/http/page"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
)

// The use-case contracts the handlers call. usecases.Queries satisfies both;
// the handler tests substitute doubles.
type (
	getScorecard interface {
		GetScorecard(ctx context.Context, nodeID string) (views.NodeScorecards, error)
	}
	listScorecards interface {
		ListScorecardsByGrade(ctx context.Context, in usecases.ListByGradeInput) (views.ScorecardPage, error)
	}
)

// handleGetScorecard serves GET /scorecards/{nodeId}.
func (s *Server) handleGetScorecard(w http.ResponseWriter, r *http.Request) {
	got, err := s.GetScorecard.GetScorecard(r.Context(), chi.URLParam(r, "nodeId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nodeScorecardsDTO{NodeID: got.NodeID, Scorecards: scorecardsOf(got.Scorecards)})
}

// listFilters are the filter query parameters of GET /scorecards; `limit` and
// `cursor` are always declared (page.Unknown).
var listFilters = []string{"grade", "window"}

// handleListScorecards serves GET /scorecards?grade=&window=&limit=&cursor=.
// An undeclared parameter, a bad limit or an undecodable cursor is a 400; a
// value the domain refuses (grade, window) is a 422.
func (s *Server) handleListScorecards(w http.ResponseWriter, r *http.Request) {
	in, err := listInput(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	got, err := s.ListScorecards.ListScorecardsByGrade(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := scorecardListDTO{Items: scorecardsOf(got.Items)}
	if got.NextCursor != "" {
		// The keyset is the scorecardId, which is unique: it is its own sort key.
		out.NextCursor = page.Encode(page.Position{Sort: got.NextCursor, ID: got.NextCursor})
	}
	writeJSON(w, http.StatusOK, out)
}

func listInput(r *http.Request) (usecases.ListByGradeInput, error) {
	if name := page.Unknown(r, listFilters...); name != "" {
		return usecases.ListByGradeInput{}, detailError{fmt.Sprintf("query parameter %q is not declared by this operation", name), unknownQueryParameter}
	}
	limit, err := page.Limit(r)
	if err != nil {
		return usecases.ListByGradeInput{}, detailError{err.Error(), invalidLimit}
	}
	pos, err := page.After(r)
	if err != nil {
		return usecases.ListByGradeInput{}, detailError{err.Error(), invalidCursor}
	}
	q := r.URL.Query()
	in := usecases.ListByGradeInput{Grade: q.Get("grade"), Window: q.Get("window"), Limit: limit}
	if pos != nil {
		in.After = pos.ID
	}
	return in, nil
}
