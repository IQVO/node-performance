package kafka_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/claudioed/node-performance/internal/adapters/outbound/kafka"
	"github.com/claudioed/node-performance/internal/domain/scorecard"
	"github.com/claudioed/node-performance/internal/domain/shared"
)

func tierChanged(tier scorecard.Tier, eligible bool, value, threshold float64) scorecard.NodeTierEligibilityChanged {
	return scorecard.NodeTierEligibilityChanged{
		Header: shared.Header{Version: 9, At: computedAt}, NodeID: "node-0f3a91", Tier: tier, Eligible: eligible,
		Metric: scorecard.MetricOnTimeHandoverRate, Value: value, Threshold: threshold,
		Window: scorecard.Window7d, ChangedAt: computedAt,
	}
}

// TestGoldenTierEligibilityEnvelopes pins the exact bytes of the suspension of
// one tier and the reinstatement of the other.
func TestGoldenTierEligibilityEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		event  shared.Event
		golden string
	}{
		{"suspension", tierChanged(scorecard.TierSameDay, false, 0.88, 0.9), "NodeTierEligibilityChanged.suspended.golden.json"},
		{"reinstatement", tierChanged(scorecard.TierNextDay, true, 0.962, 0.95), "NodeTierEligibilityChanged.reinstated.golden.json"},
	} {
		msg, err := (&kafka.Encoder{NewID: fixedID()}).Encode(tc.event)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		path := filepath.Join("testdata", tc.golden)
		if *update {
			if err := os.WriteFile(path, append(msg.Value, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := string(msg.Value) + "\n"; got != string(want) {
			t.Errorf("%s: envelope differs from %s\n got %s\nwant %s", tc.name, tc.golden, got, want)
		}
	}
}

func TestTierEligibilityChangedGoesToTheIntegrationTopic(t *testing.T) {
	msg, err := (&kafka.Encoder{NewID: fixedID()}).Encode(tierChanged(scorecard.TierSameDay, false, 0.88, 0.9))
	if err != nil {
		t.Fatal(err)
	}
	const subject = "node-0f3a91:7d"
	if msg.Topic != kafka.EventsTopic || msg.Subject != subject || string(msg.Key) != subject ||
		msg.EventType != "com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged" ||
		msg.DataSchema != "urn:retailnetwork:node-performance:events:NodeTierEligibilityChanged:v1" {
		t.Fatalf("message = %+v", msg)
	}
	assertEnvelopeOf(t, "tier eligibility", msg)
}

func TestTierEligibilityChangedIsCatalogued(t *testing.T) {
	adr, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "adr", "0004-cloudevents-envelope-and-type-catalogue.md"))
	if err != nil {
		t.Fatal(err)
	}
	msg, err := kafka.NewEncoder().Encode(tierChanged(scorecard.TierNextDay, true, 0.96, 0.95))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(adr), "type: "+msg.EventType+" stream: "+streamOf(msg)) {
		t.Errorf("ADR 0004 does not catalogue %s on stream %s", msg.EventType, streamOf(msg))
	}
}

func TestTierEligibilityChangedCarriesTheContractFieldsAndNothingElse(t *testing.T) {
	msg, err := kafka.NewEncoder().Encode(tierChanged(scorecard.TierNextDay, true, 0.96, 0.95))
	if err != nil {
		t.Fatal(err)
	}
	var env struct{ Data map[string]any }
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"nodeId": "node-0f3a91", "tier": "NEXT_DAY", "eligible": true, "metric": "onTimeHandoverRate",
		"value": 0.96, "threshold": 0.95, "window": "7d", "changedAt": "2026-10-22T14:03:00Z",
	}
	if len(env.Data) != len(want) {
		t.Fatalf("data has %d fields, want %d: %v", len(env.Data), len(want), env.Data)
	}
	for k, v := range want {
		if env.Data[k] != v {
			t.Errorf("data.%s = %v, want %v", k, env.Data[k], v)
		}
	}
}
