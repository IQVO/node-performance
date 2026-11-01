package http

import "strings"

// logReplacer strips the control characters that could forge a log line.
var logReplacer = strings.NewReplacer("\r", "", "\n", "")

// sanitizeLog strips CR and LF from a request-derived string before it
// reaches a log record, so a crafted path or error cannot forge a log line
// (CodeQL go/log-injection). Every request-derived value that is logged goes
// through it.
func sanitizeLog(s string) string {
	return logReplacer.Replace(s)
}
