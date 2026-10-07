-- Apple Watch data (J22.17, docs/adr/0024-watch-data.md#storage-no-new-tables): no new tables.
-- An ECG waveform or a workout route is a JSON document in the blob store, referenced by its
-- health_events row and counted in blobs.refcount like workouts.file_blob_sha256. Workout segments
-- gain HealthKit's multisport activities, pauses and markers. Measurements gain the context the
-- activity summary goals and the effort score's workout link need (null when there is none).
-- The Watch metric codes are seeded by the generated 00045_catalogue_watch.sql.

-- +goose Up
ALTER TABLE health_events ADD COLUMN file_blob_sha256 bytea REFERENCES blobs;
COMMENT ON COLUMN health_events.file_blob_sha256 IS 'Blob document of the event, if any: an ECG waveform (vitamux.waveform/1) or a workout route (vitamux.route/1). One blobs.refcount reference per row.';
CREATE INDEX health_events_file_idx ON health_events (file_blob_sha256) WHERE file_blob_sha256 IS NOT NULL;

ALTER TABLE workout_segments DROP CONSTRAINT workout_segments_kind_check,
  ADD CONSTRAINT workout_segments_kind_check CHECK (kind IN ('lap', 'set', 'interval', 'activity', 'pause', 'marker'));
COMMENT ON COLUMN workout_segments.kind IS 'lap, set or interval; activity (a leg of a multisport workout), pause (pause to resume) and marker (HealthKit).';

ALTER TABLE measurements ADD COLUMN context jsonb;
COMMENT ON COLUMN measurements.context IS 'Source detail kept with the value, e.g. an activity-summary goal or the workout of an effort score; null when none.';

-- +goose Down
ALTER TABLE measurements DROP COLUMN context;
DELETE FROM workout_segments WHERE kind IN ('activity', 'pause', 'marker');
ALTER TABLE workout_segments DROP CONSTRAINT workout_segments_kind_check,
  ADD CONSTRAINT workout_segments_kind_check CHECK (kind IN ('lap', 'set', 'interval'));
UPDATE blobs b SET refcount = b.refcount - c.n
FROM (SELECT file_blob_sha256, count(*)::integer AS n FROM health_events WHERE file_blob_sha256 IS NOT NULL GROUP BY file_blob_sha256) c
WHERE b.sha256 = c.file_blob_sha256;
ALTER TABLE health_events DROP COLUMN file_blob_sha256;
