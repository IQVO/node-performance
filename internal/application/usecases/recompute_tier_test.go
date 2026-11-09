package usecases_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/application/usecases"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
)

// handovers records n handovers of the node, late or on time against a
// required ship-by of minute 100, each under its own purchase order.
func (e env) handovers(t *testing.T, node, prefix string, n int, late bool) {
	t.Helper()
	ctx := context.Background()
	handedOver := 5
	if late {
		handedOver = 200
	}
	for i := 0; i < n; i++ {
		po := fmt.Sprintf("%s-po-%d", prefix, i)
		if _, err := e.intake.ShipmentConfirmed(ctx, usecases.ShipmentConfirmed{Ref: ref(po+"-ship", 1), PoNumber: po, NodeID: node, RequiredShipBy: at(100)}); err != nil {
			t.Fatalf("ship %s: %v", po, err)
		}
		if _, err := e.intake.PackageHandedOver(ctx, usecases.PackageHandedOver{Ref: ref(po+"-ho", handedOver), PoNumber: po, NodeID: node, HandedOverAt: at(handedOver)}); err != nil {
			t.Fatalf("handover %s: %v", po, err)
		}
	}
}

// capturedLogger returns a JSON logger over a buffer, and the buffer.
func capturedLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

func TestRecomputeSuspendsTheFastTiersWithTwoOutboxEventsAndOneLogLine(t *testing.T) {
	logger, buf := capturedLogger()
	e := newEnv(func(d *usecases.Deps) { d.Logger = logger })
	e.handovers(t, "node-1", "ok", 50, false)
	e.handovers(t, "node-1", "late", 10, true)
	e.clock.Advance(4 * time.Hour)
	res, err := e.recompute.Run(context.Background())
	if err != nil || res != (usecases.RecomputeResult{Recomputed: 2, Events: 4}) {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	want := []string{
		"node-1:30d NodeScorecardUpdated", "node-1:7d NodeScorecardUpdated",
		"node-1:7d NodeTierEligibilityChanged", "node-1:7d NodeTierEligibilityChanged",
	}
	if got := eventNames(e.store.Outbox().Events()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	if !e.scorecard(t, "node-1:7d").FastTier().Suspended || e.scorecard(t, "node-1:30d").FastTier().Suspended {
		t.Fatal("only the 7d scorecard holds the suspension")
	}
	recs := logRecords(t, buf)
	if len(recs) != 1 || recs[0]["msg"] != "fast tier eligibility changed" || recs[0]["level"] != "INFO" ||
		recs[0]["node_id"] != "node-1" || recs[0]["eligible"] != false || recs[0]["threshold"] != 0.9 || recs[0]["window"] != "7d" {
		t.Fatalf("log = %v, want one INFO line for the flip", recs)
	}
}

func TestRecomputeStaysQuietWhileTheFlagDoesNotFlip(t *testing.T) {
	logger, buf := capturedLogger()
	e := newEnv(func(d *usecases.Deps) { d.Logger = logger })
	e.handovers(t, "node-1", "ok", 55, false)
	e.clock.Advance(time.Hour)
	if _, err := e.recompute.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range eventNames(e.store.Outbox().Events()) {
		if strings.Contains(name, "NodeTierEligibilityChanged") {
			t.Fatalf("a node that starts eligible publishes no tier event: %s", name)
		}
	}
	if buf.Len() != 0 {
		t.Fatalf("no flip, no log line: %s", buf.String())
	}
}

func TestARolledBackRecomputeLeavesNoTierEventAndNoLogLine(t *testing.T) {
	logger, buf := capturedLogger()
	var failing *failingScorecards
	e := newEnv(func(d *usecases.Deps) {
		d.Logger = logger
		failing = &failingScorecards{ScorecardRepository: d.Scorecards, err: errBoom}
		d.Scorecards = failing
	})
	e.handovers(t, "node-1", "ok", 50, false)
	e.handovers(t, "node-1", "late", 10, true)
	e.clock.Advance(4 * time.Hour)
	failing.n = 1 << 30 // every save from now on fails
	if _, err := e.recompute.Run(context.Background()); err == nil {
		t.Fatal("the failed save must fail the run")
	}
	if n := e.store.Counts().OutboxEvents; n != 0 {
		t.Fatalf("outbox holds %d events, want none: the save and the events commit together", n)
	}
	if buf.Len() != 0 {
		t.Fatalf("a rolled-back flip is not logged: %s", buf.String())
	}
	if e.scorecard(t, "node-1:7d").FastTier() != (scorecard.FastTierState{}) {
		t.Fatal("the flag did not move")
	}
}
