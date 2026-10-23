package scorecard

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/node-performance/internal/domain/shared"
)

func TestWindows(t *testing.T) {
	got := Windows()
	if len(got) != 2 || got[0] != Window7d || got[1] != Window30d {
		t.Fatalf("Windows() = %v, want [7d 30d]", got)
	}
}

func TestParseWindow(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    Window
		wantErr bool
	}{
		{"7d", Window7d, false},
		{"30d", Window30d, false},
		{"", "", true},
		{"1d", "", true},
		{"7D", "", true},
		{"30", "", true},
	} {
		got, err := ParseWindow(tc.in)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("ParseWindow(%q) = %q, %v", tc.in, got, err)
		}
		if tc.wantErr && !errors.Is(err, shared.ErrValidation) {
			t.Errorf("ParseWindow(%q) error must wrap ErrValidation: %v", tc.in, err)
		}
	}
}

func TestWindowDuration(t *testing.T) {
	for _, tc := range []struct {
		w     Window
		want  time.Duration
		valid bool
	}{
		{Window7d, 7 * 24 * time.Hour, true},
		{Window30d, 30 * 24 * time.Hour, true},
		{Window("1d"), 0, false},
		{Window(""), 0, false},
	} {
		if got := tc.w.Duration(); got != tc.want {
			t.Errorf("%q.Duration() = %v, want %v", tc.w, got, tc.want)
		}
		if got := tc.w.Valid(); got != tc.valid {
			t.Errorf("%q.Valid() = %v, want %v", tc.w, got, tc.valid)
		}
	}
}

func TestNewNodeID(t *testing.T) {
	for _, ok := range []string{"n", "N-0042", "node-0f3a91", "a.b_c-d", "n" + strings.Repeat("x", shared.MaxID-1)} {
		if got, err := NewNodeID(ok); err != nil || string(got) != ok {
			t.Errorf("NewNodeID(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "-n", "a:b", ":", "a b", "a/b", "n" + strings.Repeat("x", shared.MaxID)} {
		if _, err := NewNodeID(bad); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("NewNodeID(%q) = %v, want ErrValidation", bad, err)
		}
	}
}

func TestScorecardID(t *testing.T) {
	if got := ScorecardID("N-0042", Window30d); got != "N-0042:30d" {
		t.Fatalf("ScorecardID = %q", got)
	}
}
