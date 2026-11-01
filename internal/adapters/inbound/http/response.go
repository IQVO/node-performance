package http

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// maxBodyBytes bounds a request body the NUL check reads: this service takes
// no request body, so anything larger is refused as malformed.
const maxBodyBytes = 64 << 10

type statusBody struct {
	Status string `json:"status"`
}

// problemDetail is the RFC 9457 application/problem+json body.
type problemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}

const (
	contentJSON    = "application/json"
	contentProblem = "application/problem+json"
)

// writeJSON encodes v as the response body with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes err as its RFC 9457 problem. An unmapped error is a 500
// whose detail never leaks the internal message (it is logged instead).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problemFor(err)
	detail := err.Error()
	if p == internalProblem {
		slog.ErrorContext(r.Context(), "request failed", "error", sanitizeLog(err.Error()), "method", r.Method, "path", sanitizeLog(r.URL.Path))
		detail = "an unexpected error occurred"
	}
	writeProblem(w, r, p, detail)
}

// writeProblem writes p with an explicit detail.
func writeProblem(w http.ResponseWriter, r *http.Request, p problem, detail string) {
	w.Header().Set("Content-Type", contentProblem)
	w.WriteHeader(p.status)
	_ = json.NewEncoder(w).Encode(problemDetail{
		Type: p.slug, Title: p.title, Status: p.status, Detail: detail, Instance: r.URL.EscapedPath(),
	})
}
