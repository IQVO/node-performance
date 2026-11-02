// Package http is the inbound REST adapter: a chi router implementing every
// operation of apis/openapi.yaml (two reads), the NUL-rejection middleware,
// RFC 9457 problem+json errors and the /healthz and /readyz probes. It depends
// on the application layer only; the composition root (cmd/api) hands it the
// use cases and the readiness gate. There is no state-changing route, so there
// is no Idempotency-Key middleware (ADR 0002, A21).
package http

import (
	"errors"
	"net/http"

	"github.com/claudioed/node-performance/internal/application/ports"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

// errMalformedRequest marks a body or query the adapter itself rejects (a bad
// number, an unknown parameter, a NUL character).
var errMalformedRequest = errors.New("request is not valid for this operation")

// problem is the fixed (status, slug, title) of one error category; the detail
// comes from the error at write time. The slug is the `type` of the RFC 9457
// body (apis/openapi.yaml Problem).
type problem struct {
	status int
	slug   string
	title  string
}

var (
	internalProblem = problem{http.StatusInternalServerError, "internal-error", "Internal server error"}

	routeNotFound    = problem{http.StatusNotFound, "not-found", "Not found"}
	methodNotAllowed = problem{http.StatusMethodNotAllowed, "method-not-allowed", "Method not allowed"}

	malformedBody  = problem{http.StatusBadRequest, "malformed-request-body", "Malformed request body"}
	malformedQuery = problem{http.StatusBadRequest, "malformed-request-query", "Malformed query string"}

	invalidLimit          = problem{http.StatusBadRequest, "invalid-limit", "Invalid limit"}
	invalidCursor         = problem{http.StatusBadRequest, "invalid-cursor", "Invalid cursor"}
	unknownQueryParameter = problem{http.StatusBadRequest, "unknown-query-parameter", "Unknown query parameter"}

	dependencyUnavailable = problem{http.StatusServiceUnavailable, "dependency-unavailable", "A runtime dependency is unavailable"}
)

// Typed errors the adapter raises itself, each carrying its own detail.
var (
	errNULQuery error = detailError{"a NUL character (U+0000) is not allowed in the query string", malformedQuery}
	errNULBody  error = detailError{"a NUL character (U+0000) is not allowed in a request body", malformedBody}
)

// detailError is an adapter error that already knows its problem.
type detailError struct {
	detail string
	p      problem
}

func (e detailError) Error() string { return e.detail }

// problemCatalogue maps every typed error to its problem, in match order: the
// two categories of the model (not-found, validation-failed) and the
// concurrency conflict the application layer can surface.
var problemCatalogue = []struct {
	err error
	p   problem
}{
	{errMalformedRequest, malformedQuery},
	{ports.ErrNotFound, routeNotFound},
	{ports.ErrConcurrentUpdate, problem{http.StatusConflict, "concurrent-update", "The resource was modified by another request; re-fetch it and retry"}},
	{shared.ErrValidation, problem{http.StatusUnprocessableEntity, "validation-failed", "Validation failed"}},
}

// problemFor returns the problem of err: the adapter's own detailError first,
// then the first catalogue entry err matches, then the internal-error problem.
func problemFor(err error) problem {
	var de detailError
	if errors.As(err, &de) {
		return de.p
	}
	for _, entry := range problemCatalogue {
		if errors.Is(err, entry.err) {
			return entry.p
		}
	}
	return internalProblem
}
