package outbox_test

import (
	"errors"
	"testing"

	"github.com/claudioed/node-performance/internal/application/outbox"
)

func TestSendFailureReportsTheRowAndUnwrapsTheCause(t *testing.T) {
	cause := errors.New("broker down")
	f := &outbox.SendFailure{RowID: 42, Message: outbox.Message{EventType: "x.NodeScorecardUpdated"}, Attempts: 3, Err: cause}
	if got, want := f.Error(), "outbox row 42 (x.NodeScorecardUpdated): broker down"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(f, cause) {
		t.Fatal("the send error must be reachable with errors.Is")
	}
	var as *outbox.SendFailure
	if !errors.As(error(f), &as) || as.Attempts != 3 {
		t.Fatalf("errors.As = %+v", as)
	}
}
