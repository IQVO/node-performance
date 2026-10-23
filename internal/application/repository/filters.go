// Package repository holds the filter structs the ports take: ports itself is
// interfaces only (enforced by internal/architecture).
package repository

import "github.com/claudioed/node-performance/internal/domain/scorecard"

// ScorecardFilter selects the scorecards of one grade.
type ScorecardFilter struct {
	// Grade is the wire grade: A, B, C, D or PROVISIONAL.
	Grade string
	// Window restricts the list to one window; empty means both.
	Window scorecard.Window
	// Limit is the page size, already validated (1 to 500).
	Limit int
	// After is the scorecardId of the last row of the previous page; empty
	// for the first page.
	After string
}
