//go:build integration

package postgres_test

import "testing"

const fastTierScorecard = `INSERT INTO scorecards (scorecard_id, node_id, scorecard_window, state, grade, on_time_handover_rate,
	late_ack_rate, reject_rate, expiry_rate, promise_kept_rate, feed_freshness_p95_minutes, observations, dirty, version,
	fast_tier_suspended, fast_tier_reinstate_streak, fast_tier_reinstate_day)
	VALUES ($1, split_part($1, ':', 1), $2, 'PROVISIONAL', NULL, 1, 0, 0, 0, 1, 0, 3, false, 1, $3, $4, $5::date)`

// TestTheSchemaRefusesAnInconsistentFastTierState: the flag lives on 7d rows,
// the streak only while suspended, and the streak and its day go together.
func TestTheSchemaRefusesAnInconsistentFastTierState(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id, win   string
		suspended bool
		streak    int
		day       any
		want      bool // true: the insert must succeed
	}{
		{"a suspended 7d row", "n:7d", "7d", true, 0, nil, true},
		{"a suspended 7d row with a streak", "m:7d", "7d", true, 2, "2026-10-23", true},
		{"a 30d row left eligible", "n:30d", "30d", false, 0, nil, true},
		{"a suspended 30d row", "x:30d", "30d", true, 0, nil, false},
		{"a streak of three", "y:7d", "7d", true, 3, "2026-10-23", false},
		{"a negative streak", "y:7d", "7d", true, -1, "2026-10-23", false},
		{"a streak while eligible", "y:7d", "7d", false, 1, "2026-10-23", false},
		{"a streak without a day", "y:7d", "7d", true, 1, nil, false},
		{"a day without a streak", "y:7d", "7d", true, 0, "2026-10-23", false},
	} {
		err := execErr(t, fastTierScorecard, tc.id, tc.win, tc.suspended, tc.streak, tc.day)
		if (err == nil) != tc.want {
			t.Errorf("%s: err = %v, want success %v", tc.name, err, tc.want)
		}
	}
}
