package http

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Server holds everything the router needs; cmd/api fills every field.
type Server struct {
	GetScorecard   getScorecard
	ListScorecards listScorecards

	// Readiness backs /readyz; the zero gate is ready with no dependency.
	Readiness *Readiness
	// Logger receives the access log; slog.Default when nil.
	Logger *slog.Logger
}

// ErrIncompleteServer is returned by NewRouter when a use case is missing: a
// nil one would be a nil-pointer panic on the first request.
var ErrIncompleteServer = errors.New("http server: every use case is required")

// NewRouter builds the chi router. Order of the middleware, outermost first:
// access log, recover (a panic is a 500 problem the log still sees), rejectNUL,
// then the route.
func NewRouter(s *Server) (http.Handler, error) {
	if s.GetScorecard == nil || s.ListScorecards == nil {
		return nil, ErrIncompleteServer
	}
	r := chi.NewRouter()
	r.Use(accessLog(s.Logger), recoverer, rejectNUL)
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

// accessLog logs one line per request with its status and duration.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			logger.LogAttrs(r.Context(), levelFor(r.URL.Path, status), "request",
				slog.String("method", r.Method), slog.String("path", r.URL.EscapedPath()),
				slog.Int("status", status), slog.Duration("duration", time.Since(start)))
		})
	}
}

// levelFor keeps the probes quiet and flags server failures.
func levelFor(path string, status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case path == "/healthz" || path == "/readyz":
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}
