package postgres

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/claudioed/node-performance/internal/application/ports"
)

// SQLSTATE of a unique violation.
const pgUniqueViolation = "23505"

// pkScorecards is the primary key of scorecards: a violation means the id is
// already stored.
const pkScorecards = "scorecards_pkey"

// uniqueViolation returns the name of the constraint that err violated, if err
// is a Postgres unique violation.
func uniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// translateCreate maps the primary-key violation of a create (the id already
// exists) to a concurrent update; any other error is wrapped with what the
// write was doing.
func translateCreate(err error, what string) error {
	if name, ok := uniqueViolation(err); ok && name == pkScorecards {
		return fmt.Errorf("%w: %s already stored", ports.ErrConcurrentUpdate, what)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// errStaleVersion is the miss of an UPDATE ... WHERE version = n: the row has
// another version or no longer exists.
func errStaleVersion(what string, want int64) error {
	return fmt.Errorf("%w: %s is not stored at version %d", ports.ErrConcurrentUpdate, what, want)
}
