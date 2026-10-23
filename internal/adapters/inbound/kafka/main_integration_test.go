//go:build integration

package kafka

import (
	"os"
	"testing"

	_ "github.com/testcontainers/testcontainers-go/modules/kafka" // the broker comes from internal/testing/kafkatest

	"github.com/claudioed/node-performance/internal/testing/kafkatest"
	"github.com/claudioed/node-performance/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}
