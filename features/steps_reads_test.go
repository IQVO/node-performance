package features_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/application/views"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// registerReadSteps registers the steps of the two read operations of
// apis/openapi.yaml: reading a node's scorecards and listing a grade with the
// window filter, the page size and the cursor of the keyset pagination.
func registerReadSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^reading the node "([^"]*)" returns the scorecards "([^"]*)"$`, w.readingNodeReturns)
	sc.Step(`^reading the node "([^"]*)" is refused as invalid$`, w.readingNodeRefused)
	sc.Step(`^listing the grade "([^"]*)" in window "([^"]*)" returns "([^"]*)"$`, w.listingInWindowReturns)
	sc.Step(`^listing the grade "([^"]*)" in window "([^"]*)" is refused as invalid$`, w.listingWindowRefused)
	sc.Step(`^listing the grade "([^"]*)" with a page of (\d+) returns "([^"]*)" and the next cursor "([^"]*)"$`, w.listingPageCursor)
	sc.Step(`^listing the grade "([^"]*)" with a page of (\d+) returns "([^"]*)" and no next cursor$`, w.listingPageNoCursor)
	sc.Step(`^listing the grade "([^"]*)" after "([^"]*)" with a page of (\d+) returns "([^"]*)" and no next cursor$`, w.listingAfterPageNoCursor)
	sc.Step(`^listing the grade "([^"]*)" with a page of (\d+) is refused as invalid$`, w.listingPageRefused)
	sc.Step(`^listing after a cursor of (\d+) characters is refused as invalid$`, w.listingCursorRefused)
}

func idsOf(items []views.Scorecard) string {
	ids := make([]string, len(items))
	for i, v := range items {
		ids[i] = v.ScorecardID
	}
	return strings.Join(ids, ",")
}

// listPage runs ListScorecardsByGrade once with in and returns the page.
func (w *world) listPage(in usecases.ListByGradeInput) (views.ScorecardPage, error) {
	return w.queries.ListScorecardsByGrade(w.ctx(), in)
}

// wantIDs fails unless the page holds exactly the ids, in order.
func wantIDs(page views.ScorecardPage, want string) error {
	if got := idsOf(page.Items); got != want {
		return fmt.Errorf("the page lists %q, want %q", got, want)
	}
	return nil
}

// refused fails unless err is the validation error of the read model.
func refused(err error, what string) error {
	if !errors.Is(err, shared.ErrValidation) {
		return fmt.Errorf("%s = %v, want a validation error", what, err)
	}
	return nil
}

func (w *world) readingNodeReturns(node, want string) error {
	got, err := w.queries.GetScorecard(w.ctx(), node)
	if err != nil {
		return err
	}
	return wantIDs(views.ScorecardPage{Items: got.Scorecards}, want)
}

func (w *world) readingNodeRefused(node string) error {
	_, err := w.queries.GetScorecard(w.ctx(), node)
	return refused(err, fmt.Sprintf("reading node %q", node))
}

func (w *world) listingInWindowReturns(grade, window, want string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: grade, Window: window})
	if err != nil {
		return err
	}
	return wantIDs(page, want)
}

func (w *world) listingWindowRefused(grade, window string) error {
	_, err := w.listPage(usecases.ListByGradeInput{Grade: grade, Window: window})
	return refused(err, fmt.Sprintf("window %q", window))
}

func (w *world) listingPageCursor(grade string, limit int, want, cursor string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: grade, Limit: limit})
	if err != nil {
		return err
	}
	if err := wantIDs(page, want); err != nil {
		return err
	}
	if page.NextCursor != cursor {
		return fmt.Errorf("the next cursor is %q, want %q", page.NextCursor, cursor)
	}
	return nil
}

func (w *world) listingPageNoCursor(grade string, limit int, want string) error {
	return w.listingPageCursor(grade, limit, want, "")
}

func (w *world) listingAfterPageNoCursor(grade, after string, limit int, want string) error {
	page, err := w.listPage(usecases.ListByGradeInput{Grade: grade, After: after, Limit: limit})
	if err != nil {
		return err
	}
	if err := wantIDs(page, want); err != nil {
		return err
	}
	if page.NextCursor != "" {
		return fmt.Errorf("the last page still advertises the cursor %q", page.NextCursor)
	}
	return nil
}

func (w *world) listingPageRefused(grade string, limit int) error {
	_, err := w.listPage(usecases.ListByGradeInput{Grade: grade, Limit: limit})
	return refused(err, fmt.Sprintf("a page of %d", limit))
}

func (w *world) listingCursorRefused(n int) error {
	cursor := strings.Repeat("a", n)
	_, err := w.listPage(usecases.ListByGradeInput{Grade: "A", After: cursor})
	return refused(err, fmt.Sprintf("a cursor of %d characters", n))
}
