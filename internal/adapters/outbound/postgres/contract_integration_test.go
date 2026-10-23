//go:build integration

package postgres_test

import (
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/repocontract"
)

// TestRepositoryContract runs the SAME contract suite the in-memory doubles
// pass, against the Postgres adapters, one fresh migrated database per case.
func TestRepositoryContract(t *testing.T) {
	repocontract.Run(t, func(t *testing.T) repocontract.Fixture {
		f, _ := newFixture(t)
		return f
	})
}
