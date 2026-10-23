package http_test

import (
	"io"
	"log/slog"
	"os"
	"testing"
)

// The handlers log unmapped errors through slog.Default; keep the test output
// readable.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}
