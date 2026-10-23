package scorecard

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestPurchaseOrderFactsValidate(t *testing.T) {
	ok := PurchaseOrderFacts{PoNumber: "po-8a31", NodeID: "node-1"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid facts: %v", err)
	}
	if err := (PurchaseOrderFacts{PoNumber: strings.Repeat("p", shared.MaxID), NodeID: "node-1"}).Validate(); err != nil {
		t.Fatalf("a %d-character po number is valid: %v", shared.MaxID, err)
	}
	for name, f := range map[string]PurchaseOrderFacts{
		"no po number":       {NodeID: "node-1"},
		"po number too long": {PoNumber: strings.Repeat("p", shared.MaxID+1), NodeID: "node-1"},
		"padded po number":   {PoNumber: " po", NodeID: "node-1"},
		"no node":            {PoNumber: "po-1"},
		"node with colon":    {PoNumber: "po-1", NodeID: "a:b"},
	} {
		if err := f.Validate(); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestPurchaseOrderFactsMergeOnlyFills(t *testing.T) {
	a, b, c := at(1), at(2), at(3)
	old := PurchaseOrderFacts{PoNumber: "po-1", NodeID: "n", IssuedAt: a}
	got := old.Merge(PurchaseOrderFacts{IssuedAt: b, AckBy: b, RequiredShipBy: c})
	want := PurchaseOrderFacts{PoNumber: "po-1", NodeID: "n", IssuedAt: a, AckBy: b, RequiredShipBy: c}
	if got != want {
		t.Fatalf("Merge = %+v, want %+v", got, want)
	}
	full := PurchaseOrderFacts{PoNumber: "po-1", NodeID: "n", IssuedAt: a, AckBy: b, RequiredShipBy: c}
	if again := full.Merge(PurchaseOrderFacts{IssuedAt: c, AckBy: c, RequiredShipBy: a}); again != full {
		t.Fatalf("a filled fact is never overwritten: %+v", again)
	}
	if empty := old.Merge(PurchaseOrderFacts{}); empty != old {
		t.Fatalf("merging nothing changes nothing: %+v", empty)
	}
	if only := (PurchaseOrderFacts{}).Merge(PurchaseOrderFacts{AckBy: b}); only.AckBy != b || !only.IssuedAt.IsZero() || !only.RequiredShipBy.IsZero() {
		t.Fatalf("each field merges on its own: %+v", only)
	}
}

func TestPurchaseOrderFactsJudgements(t *testing.T) {
	zero := time.Time{}
	for _, tc := range []struct {
		name          string
		f             PurchaseOrderFacts
		ack, handover bool
	}{
		{"nothing", PurchaseOrderFacts{}, false, false},
		{"issued only", PurchaseOrderFacts{IssuedAt: at(1)}, false, false},
		{"ackBy only", PurchaseOrderFacts{AckBy: at(1)}, false, false},
		{"issued and ackBy", PurchaseOrderFacts{IssuedAt: at(1), AckBy: at(2)}, true, false},
		{"requiredShipBy only", PurchaseOrderFacts{RequiredShipBy: at(3), IssuedAt: zero}, false, true},
		{"everything", PurchaseOrderFacts{IssuedAt: at(1), AckBy: at(2), RequiredShipBy: at(3)}, true, true},
	} {
		if got := tc.f.CanJudgeAck(); got != tc.ack {
			t.Errorf("%s: CanJudgeAck = %v", tc.name, got)
		}
		if got := tc.f.CanJudgeHandover(); got != tc.handover {
			t.Errorf("%s: CanJudgeHandover = %v", tc.name, got)
		}
	}
}

func TestPurchaseOrderFactsNormalized(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	fine := time.Date(2026, 10, 22, 9, 0, 0, 123456789, brt)
	got := (PurchaseOrderFacts{PoNumber: "po-1", NodeID: "n", IssuedAt: fine, AckBy: fine, RequiredShipBy: fine}).Normalized()
	want := time.Date(2026, 10, 22, 12, 0, 0, 123456000, time.UTC)
	for name, v := range map[string]time.Time{"issuedAt": got.IssuedAt, "ackBy": got.AckBy, "requiredShipBy": got.RequiredShipBy} {
		if !v.Equal(want) || v.Location() != time.UTC {
			t.Errorf("%s = %v, want %v", name, v, want)
		}
	}
	if zero := (PurchaseOrderFacts{PoNumber: "po-1"}).Normalized(); !zero.IssuedAt.IsZero() || !zero.AckBy.IsZero() {
		t.Fatal("a missing instant stays zero")
	}
}
