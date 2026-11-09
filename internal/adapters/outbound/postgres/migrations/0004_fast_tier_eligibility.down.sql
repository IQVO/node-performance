DROP INDEX IF EXISTS idx_scorecards_fast_tier_suspended;
ALTER TABLE scorecards
    DROP CONSTRAINT IF EXISTS scorecards_fast_tier_streak,
    DROP CONSTRAINT IF EXISTS scorecards_fast_tier_window,
    DROP COLUMN IF EXISTS fast_tier_reinstate_day,
    DROP COLUMN IF EXISTS fast_tier_reinstate_streak,
    DROP COLUMN IF EXISTS fast_tier_suspended;
