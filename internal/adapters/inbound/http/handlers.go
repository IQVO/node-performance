package http

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/shared"
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

// listParams are the query parameters of GET /scorecards.
var listParams = map[string]bool{"grade": true, "window": true, "limit": true, "after": true}

// handleListScorecards serves GET /scorecards?grade=&window=&limit=&after=.
// An unknown parameter or a limit that is not an integer is a 400; a value the
// domain refuses (grade, window, limit range, cursor) is a 422.
func (s *Server) handleListScorecards(w http.ResponseWriter, r *http.Request) {
	in, err := listInput(r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := s.ListScorecards.ListScorecardsByGrade(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, scorecardListDTO{Items: scorecardsOf(page.Items), NextCursor: page.NextCursor})
}

func listInput(q url.Values) (usecases.ListByGradeInput, error) {
	for name := range q {
		if !listParams[name] {
			return usecases.ListByGradeInput{}, fmt.Errorf("%w: unknown query parameter %q", errMalformedRequest, name)
		}
	}
	in := usecases.ListByGradeInput{Grade: q.Get("grade"), Window: q.Get("window"), After: q.Get("after")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return usecases.ListByGradeInput{}, fmt.Errorf("%w: limit must be an integer", errMalformedRequest)
		}
		if n < 1 {
			// 0 would read as "absent" in the use case; a present limit below 1 is refused.
			return usecases.ListByGradeInput{}, shared.Invalid("limit must be between 1 and %d", usecases.MaxListLimit)
		}
		in.Limit = n
	}
	return in, nil
}
