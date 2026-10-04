-- A paced backfill starts at most daily_limit units a UTC day (J25.6: the Garmin cold-storage reload);
-- NULL is the unpaced backfill of J06.6.

-- +goose Up
ALTER TABLE backfills ADD COLUMN daily_limit integer CHECK (daily_limit > 0);

-- +goose Down
ALTER TABLE backfills DROP COLUMN daily_limit;
