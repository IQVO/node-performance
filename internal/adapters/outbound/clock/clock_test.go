package clock_test

import (
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/adapters/outbound/clock"
)

func TestSystemClockIsUTCAndMicrosecondPrecise(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Microsecond)
	got := clock.System{}.Now()
	after := time.Now().UTC()
	if got.Location() != time.UTC {
		t.Fatalf("location = %v, want UTC", got.Location())
	}
	if got.Nanosecond()%1000 != 0 {
		t.Fatalf("%d ns: not truncated to the microsecond", got.Nanosecond())
	}
	if got.Before(before) || got.After(after) {
		t.Fatalf("now = %v, want between %v and %v", got, before, after)
	}
}
