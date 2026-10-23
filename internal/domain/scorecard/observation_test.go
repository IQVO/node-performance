package scorecard

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestAckIsLate(t *testing.T) {
	// The ack window is 12:00-22:00 (10 hours): the last 10% starts at 21:00.
	issued, ackBy := at(0), at(600)
	for _, tc := range []struct {
		name  string
		acked time.Time
		want  bool
	}{
		{"well before the late part", at(60), false},
		{"a nanosecond before the late part", at(540).Add(-time.Nanosecond), false},
		{"exactly at the start of the late part", at(540), true},
		{"inside the late part", at(570), true},
		{"exactly at ackBy", at(600), true},
		{"after ackBy", at(601), true},
		{"before the order was issued", at(-5), false},
	} {
		if got := AckIsLate(issued, ackBy, tc.acked); got != tc.want {
			t.Errorf("%s: AckIsLate = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A window that ends before it starts counts as empty: only ackBy or later is late.
	if AckIsLate(at(60), at(0), at(0).Add(-time.Nanosecond)) {
		t.Error("an instant before ackBy of an inverted window is not late")
	}
	if !AckIsLate(at(60), at(0), at(0)) {
		t.Error("ackBy of an inverted window is late")
	}
}

func TestNewAcknowledgement(t *testing.T) {
	issued, ackBy := at(0), at(600)
	for _, tc := range []struct {
		name     string
		kind     Kind
		acked    time.Time
		wantLate bool
	}{
		{"early acceptance", KindAckAccepted, at(30), false},
		{"late acceptance", KindAckAccepted, at(560), true},
		{"early rejection", KindAckRejected, at(30), false},
		{"late rejection", KindAckRejected, at(540), true},
	} {
		o, err := NewAcknowledgement("evt-1", tc.kind, tc.acked, issued, ackBy)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if o.Kind != tc.kind || o.Late != tc.wantLate || !o.At.Equal(tc.acked) || o.SourceEventID != "evt-1" || o.OnTime {
			t.Errorf("%s: observation = %+v", tc.name, o)
		}
	}
}

func TestNewAcknowledgementRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   Kind
		id     string
		acked  time.Time
		issued time.Time
		ackBy  time.Time
	}{
		{"expiry kind", KindAckExpired, "e", at(1), at(0), at(60)},
		{"handover kind", KindHandover, "e", at(1), at(0), at(60)},
		{"no issuedAt", KindAckAccepted, "e", at(1), time.Time{}, at(60)},
		{"no ackBy", KindAckAccepted, "e", at(1), at(0), time.Time{}},
		{"no source id", KindAckAccepted, "", at(1), at(0), at(60)},
		{"no instant", KindAckRejected, "e", time.Time{}, at(0), at(60)},
	} {
		if _, err := NewAcknowledgement(tc.id, tc.kind, tc.acked, tc.issued, tc.ackBy); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", tc.name, err)
		}
	}
}

func TestNewExpiry(t *testing.T) {
	o, err := NewExpiry("evt-2", at(600))
	if err != nil {
		t.Fatal(err)
	}
	if o.Kind != KindAckExpired || !o.Late || !o.At.Equal(at(600)) || o.OnTime {
		t.Fatalf("expiry = %+v", o)
	}
	if _, err := NewExpiry("evt-2", time.Time{}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("an expiry without ackBy must be refused: %v", err)
	}
}

func TestNewHandover(t *testing.T) {
	required := at(600)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"early", at(10), true},
		{"a microsecond before", required.Add(-time.Microsecond), true},
		{"exactly on requiredShipBy", required, true},
		{"a microsecond after", required.Add(time.Microsecond), false},
		{"much later", at(1200), false},
	} {
		o, err := NewHandover("evt-3", tc.at, required)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if o.OnTime != tc.want || o.Kind != KindHandover || o.Late || !o.At.Equal(tc.at) {
			t.Errorf("%s: handover = %+v, want onTime %v", tc.name, o, tc.want)
		}
	}
	if _, err := NewHandover("evt-3", at(1), time.Time{}); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a handover without requiredShipBy must be refused: %v", err)
	}
	if _, err := NewHandover("evt-3", time.Time{}, required); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("a handover without an instant must be refused: %v", err)
	}
}

func TestPlainObservationConstructors(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind Kind
		make func(string, time.Time) (Observation, error)
	}{
		{"kept", KindPromiseKept, NewPromiseKept},
		{"broken", KindPromiseBroken, NewPromiseBroken},
		{"feed", KindFeedApplied, NewFeedApplied},
	} {
		o, err := tc.make("evt-4", at(5))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if o.Kind != tc.kind || !o.At.Equal(at(5)) || o.SourceEventID != "evt-4" || o.Late || o.OnTime {
			t.Errorf("%s: observation = %+v", tc.name, o)
		}
		if _, err := tc.make("evt-4", time.Time{}); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: no instant must be refused: %v", tc.name, err)
		}
	}
}

func TestObservationInstantIsUTC(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	o, err := NewFeedApplied("evt-5", time.Date(2026, 10, 22, 9, 0, 0, 0, brt))
	if err != nil {
		t.Fatal(err)
	}
	if o.At.Location() != time.UTC || !o.At.Equal(base) {
		t.Fatalf("At = %v, want %v in UTC", o.At, base)
	}
}

func TestSourceEventIDBounds(t *testing.T) {
	ok := strings.Repeat("e", MaxSourceEventID)
	if _, err := NewFeedApplied(ok, at(1)); err != nil {
		t.Fatalf("a %d-character id must be accepted: %v", MaxSourceEventID, err)
	}
	for _, bad := range []string{"", " e", "e ", "e\n", strings.Repeat("e", MaxSourceEventID+1)} {
		if _, err := NewFeedApplied(bad, at(1)); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("id %q must be refused: %v", bad, err)
		}
	}
}

func TestUnknownKindIsRefused(t *testing.T) {
	if err := (Observation{SourceEventID: "e", Kind: "NOPE", At: at(1)}).validate(); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("unknown kind: %v", err)
	}
	for _, k := range []Kind{KindAckAccepted, KindAckRejected, KindAckExpired, KindHandover, KindPromiseKept, KindPromiseBroken, KindFeedApplied} {
		if err := (Observation{SourceEventID: "e", Kind: k, At: at(1)}).validate(); err != nil {
			t.Errorf("kind %s must be valid: %v", k, err)
		}
	}
}

func TestAttributableCause(t *testing.T) {
	for cause, want := range map[string]bool{
		"LATE": true, "LATE_HANDOVER": true, "VENDOR_REJECT": true, "ACK_EXPIRED": true,
		"LOST": false, "RETURNED": false, "": false, "late": false, "SOMETHING_NEW": false,
	} {
		if got := AttributableCause(cause); got != want {
			t.Errorf("AttributableCause(%q) = %v, want %v", cause, got, want)
		}
	}
}
