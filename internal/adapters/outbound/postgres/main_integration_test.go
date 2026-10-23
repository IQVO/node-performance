//go:build integration

package postgres_test

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	shutdownContainer()
	os.Exit(code)
}
