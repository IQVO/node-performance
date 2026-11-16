package http_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource does support. chi sets Allow only in its
// DEFAULT 405 handler; once a custom handler is registered (the RFC 7807 one)
// it is called as-is, so the handler derives the list from the router itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	h := router(t, realServer(t))
	for _, tc := range []struct{ method, path, wantAllow string }{
		{http.MethodPost, "/healthz", "GET"},
		{http.MethodDelete, "/readyz", "GET"},
		{http.MethodPut, "/scorecards", "GET"},
		{http.MethodPost, "/scorecards/node-a", "GET"},
		{http.MethodDelete, "/scorecards/node-a", "GET"},
		{http.MethodPatch, "/scorecards/node-a", "GET"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
			r := serve(h, req)
			if r.status != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405: %s", r.status, r.raw)
			}
			if got := r.header.Get("Content-Type"); got != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", got)
			}
			if got := r.header.Get("Allow"); got != tc.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tc.wantAllow)
			}
		})
	}
}

// TestUnknownPathHasNoAllowHeader: a 404 is not a 405, so it lists no methods.
func TestUnknownPathHasNoAllowHeader(t *testing.T) {
	r := serve(router(t, realServer(t)), httptest.NewRequest(http.MethodDelete, "/nowhere", http.NoBody))
	if r.status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", r.status)
	}
	if got := r.header.Get("Allow"); got != "" {
		t.Errorf("Allow = %q on an unknown path, want empty", got)
	}
}
