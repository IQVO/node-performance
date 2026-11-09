-- FastTierEligibility state of the 7d scorecard (ADR 0013).
--   fast_tier_suspended        the node is ineligible for the SAME_DAY and NEXT_DAY tiers.
--                              Every row starts false (eligible); only a 7d row can be true.
--   fast_tier_reinstate_streak distinct UTC days, in a row, of a GRADED recompute at or above
--                              the reinstatement rate (0 to 2: the third day reinstates).
--   fast_tier_reinstate_day    the last UTC day counted in the streak; NULL while the streak is 0.
ALTER TABLE scorecards
    ADD COLUMN fast_tier_suspended        BOOLEAN  NOT NULL DEFAULT false,
    ADD COLUMN fast_tier_reinstate_streak SMALLINT NOT NULL DEFAULT 0 CHECK (fast_tier_reinstate_streak BETWEEN 0 AND 2),
    ADD COLUMN fast_tier_reinstate_day    DATE,
    ADD CONSTRAINT scorecards_fast_tier_window CHECK (NOT fast_tier_suspended OR scorecard_window = '7d'),
    ADD CONSTRAINT scorecards_fast_tier_streak CHECK (
        (fast_tier_suspended OR fast_tier_reinstate_streak = 0)
        AND ((fast_tier_reinstate_streak = 0) = (fast_tier_reinstate_day IS NULL))
    );

-- The gauge nodeperf_fast_tier_ineligible_nodes counts the suspended nodes.
CREATE INDEX idx_scorecards_fast_tier_suspended ON scorecards (scorecard_id) WHERE fast_tier_suspended;
