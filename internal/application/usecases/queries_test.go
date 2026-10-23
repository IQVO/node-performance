package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/application/repository"
	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestGetScorecardReturnsBothWindowsOfTheNode(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 2)
	e.feedN(t, "node-2", 1)
	got, err := e.queries.GetScorecard(context.Background(), "node-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeID != "node-1" || len(got.Scorecards) != 2 || got.Scorecards[0].Window != "7d" || got.Scorecards[1].Window != "30d" {
		t.Fatalf("GetScorecard = %+v", got)
	}
	if got.Scorecards[0].Grade != "PROVISIONAL" || got.Scorecards[0].State != "PROVISIONAL" {
		t.Fatalf("view = %+v", got.Scorecards[0])
	}
}

func TestGetScorecardRefusals(t *testing.T) {
	e := newEnv()
	ctx := context.Background()
	if _, err := e.queries.GetScorecard(ctx, "node-ghost"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("unknown node: %v", err)
	}
	for _, bad := range []string{"", "a:b", "-x", strings.Repeat("n", shared.MaxID+1)} {
		if _, err := e.queries.GetScorecard(ctx, bad); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("node id %q: %v", bad, err)
		}
	}
	failing := usecases.Queries{Reader: failingReader{err: errBoom}}
	if _, err := failing.GetScorecard(ctx, "node-1"); !errors.Is(err, errBoom) {
		t.Fatalf("reader failure: %v", err)
	}
}

// recordingReader records the filter ByGrade got and answers a fixed page.
type recordingReader struct {
	ports.ScorecardReader
	got  repository.ScorecardFilter
	page views.ScorecardPage
}

func (r *recordingReader) ByGrade(_ context.Context, f repository.ScorecardFilter) (views.ScorecardPage, error) {
	r.got = f
	return r.page, nil
}

func TestListScorecardsByGradePassesAValidatedFilter(t *testing.T) {
	cursor := strings.Repeat("c", 80)
	for _, tc := range []struct {
		name string
		in   usecases.ListByGradeInput
		want repository.ScorecardFilter
	}{
		{"defaults", usecases.ListByGradeInput{Grade: "A"}, repository.ScorecardFilter{Grade: "A", Limit: 100}},
		{"every field", usecases.ListByGradeInput{Grade: "PROVISIONAL", Window: "30d", Limit: 500, After: cursor},
			repository.ScorecardFilter{Grade: "PROVISIONAL", Window: scorecard.Window30d, Limit: 500, After: cursor}},
		{"smallest page", usecases.ListByGradeInput{Grade: "D", Window: "7d", Limit: 1}, repository.ScorecardFilter{Grade: "D", Window: scorecard.Window7d, Limit: 1}},
		{"grade B", usecases.ListByGradeInput{Grade: "B"}, repository.ScorecardFilter{Grade: "B", Limit: 100}},
		{"grade C", usecases.ListByGradeInput{Grade: "C"}, repository.ScorecardFilter{Grade: "C", Limit: 100}},
	} {
		r := &recordingReader{page: views.ScorecardPage{NextCursor: "next"}}
		page, err := usecases.Queries{Reader: r}.ListScorecardsByGrade(context.Background(), tc.in)
		if err != nil || !reflect.DeepEqual(r.got, tc.want) || page.NextCursor != "next" {
			t.Errorf("%s: filter %+v page %+v err %v; want %+v", tc.name, r.got, page, err, tc.want)
		}
	}
}

func TestListScorecardsByGradeRefusals(t *testing.T) {
	for name, in := range map[string]usecases.ListByGradeInput{
		"no grade":        {},
		"unknown grade":   {Grade: "E"},
		"lower-case":      {Grade: "a"},
		"graded state":    {Grade: "GRADED"},
		"bad window":      {Grade: "A", Window: "1d"},
		"negative limit":  {Grade: "A", Limit: -1},
		"limit too large": {Grade: "A", Limit: 501},
		"cursor too long": {Grade: "A", After: strings.Repeat("c", 81)},
		"cursor control":  {Grade: "A", After: "a\nb"},
	} {
		r := &recordingReader{}
		if _, err := (usecases.Queries{Reader: r}).ListScorecardsByGrade(context.Background(), in); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
		if r.got != (repository.ScorecardFilter{}) {
			t.Errorf("%s: the reader must not be called", name)
		}
	}
}

func TestListScorecardsByGradeAgainstTheStore(t *testing.T) {
	e := newEnv()
	e.feedN(t, "node-1", 1)
	page, err := e.queries.ListScorecardsByGrade(context.Background(), usecases.ListByGradeInput{Grade: "PROVISIONAL"})
	if err != nil || len(page.Items) != 2 || page.NextCursor != "" {
		t.Fatalf("page = %+v, %v", page, err)
	}
}

// failingReader fails every query.
type failingReader struct{ err error }

func (f failingReader) ByNode(context.Context, scorecard.NodeID) ([]views.Scorecard, error) {
	return nil, f.err
}

func (f failingReader) ByGrade(context.Context, repository.ScorecardFilter) (views.ScorecardPage, error) {
	return views.ScorecardPage{}, f.err
}
