package http

import "testing"

func TestSanitizeLogStripsLineBreaks(t *testing.T) {
	if got, want := sanitizeLog("a\r\nb\nc\rd"), "abcd"; got != want {
		t.Fatalf("sanitizeLog = %q, want %q", got, want)
	}
}
