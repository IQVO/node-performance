package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"

	"github.com/claudioed/node-performance/internal/adapters/inbound/requestlog"
)

// DefaultServiceName names the server spans and the HTTP metrics when the
// composition root sets no Server.ServiceName.
const DefaultServiceName = "node-performance"

// Server holds everything the router needs; cmd/api fills every field.
type Server struct {
	GetScorecard   getScorecard
	ListScorecards listScorecards

	// Readiness backs /readyz; the zero gate is ready with no dependency.
	Readiness *Readiness
	// Logger receives the access log; slog.Default when nil.
	Logger *slog.Logger
	// ServiceName names the server spans and HTTP metrics; DefaultServiceName
	// when empty.
	ServiceName string
}

// ErrIncompleteServer is returned by NewRouter when a use case is missing: a
// nil one would be a nil-pointer panic on the first request.
var ErrIncompleteServer = errors.New("http server: every use case is required")

// NewRouter builds the chi router. Order of the middleware, outermost first (the
// fleet observability standard): RequestID, otelchi (the server span, named
// after the chi route pattern), the request-duration histogram, RequestLogger
// (inside the span, so its record carries trace_id and span_id), recover (a
// panic is a 500 problem the log still sees), rejectNUL, then the route.
// telemetry.Setup must have run first: otelchimetric binds the global
// MeterProvider when it is constructed.
func NewRouter(s *Server) (http.Handler, error) {
	if s.GetScorecard == nil || s.ListScorecards == nil {
		return nil, ErrIncompleteServer
	}
	serviceName := s.ServiceName
	if serviceName == "" {
		serviceName = DefaultServiceName
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(otelchi.Middleware(serviceName, otelchi.WithChiRoutes(r)))
	r.Use(otelchimetric.NewServerRequestDuration(otelchimetric.NewBaseConfig(serviceName)))
	r.Use(requestlog.RequestLogger(s.Logger), recoverer, rejectNUL)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, routeNotFound, "no route matches "+r.Method+" "+r.URL.EscapedPath())
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, methodNotAllowed, r.Method+" is not allowed on "+r.URL.EscapedPath())
	})

	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", readyz(s.Readiness))
	r.Get("/scorecards", s.handleListScorecards)
	r.Get("/scorecards/{nodeId}", s.handleGetScorecard)
	return r, nil
}

// recoverer turns a panic into a 500 problem and logs it.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler { //nolint:errorlint // the sentinel is compared by identity on purpose
					panic(p)
				}
				slog.ErrorContext(r.Context(), "panic in handler", "panic", p, "method", r.Method, "path", r.URL.Path)
				writeProblem(w, r, internalProblem, "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
