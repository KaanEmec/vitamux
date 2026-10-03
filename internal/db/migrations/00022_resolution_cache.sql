-- Rebuildable resolution state (J09.9): hourly source aggregates and the resolved cache, see
-- docs/architecture/resolution.md#cache-and-materialization. Truncating either table is always safe.

-- +goose Up
CREATE TABLE source_hourly_aggregates (
  user_id         uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  metric_id       smallint NOT NULL REFERENCES metric_catalog,
  source_key      text NOT NULL,
  hour_start      timestamptz NOT NULL,
  local_date      date NOT NULL,
  connection_id   uuid NOT NULL REFERENCES connections ON DELETE CASCADE,
  device_id       uuid,
  origin_id       uuid,
  samples         integer NOT NULL,
  buckets         smallint NOT NULL,
  bucket_mean_sum double precision NOT NULL,
  min_value       double precision NOT NULL,
  max_value       double precision NOT NULL,
  interval_sum    double precision NOT NULL,
  first_at        timestamptz NOT NULL,
  last_at         timestamptz NOT NULL,
  PRIMARY KEY (user_id, metric_id, hour_start, source_key)
);
COMMENT ON TABLE source_hourly_aggregates IS 'Per source and local hour of the owner''s timeline: active sample and interval rows (daily values excluded). Rebuilt from resolution_dirty by the rebuild_aggregates job.';
COMMENT ON COLUMN source_hourly_aggregates.source_key IS 'connection_id/device_id/origin_id, with - for a missing device or origin.';
COMMENT ON COLUMN source_hourly_aggregates.buckets IS 'UTC-aligned 5-minute buckets holding a sample or part of an interval; bucket_mean_sum adds the sample mean of each.';
COMMENT ON COLUMN source_hourly_aggregates.interval_sum IS 'Interval values pro-rated linearly to the hour (additive metrics).';

CREATE TABLE resolved_cache (
  user_id      uuid NOT NULL REFERENCES users ON DELETE CASCADE,
  metric       text NOT NULL,
  window_kind  text NOT NULL,
  local_date   date NOT NULL,
  rule_ref     text NOT NULL,
  overrides_fp text NOT NULL,
  results      jsonb NOT NULL,
  deps         text[] NOT NULL,
  dep_from     date NOT NULL,
  dep_to       date NOT NULL,
  complete_at  timestamptz NOT NULL,
  computed_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, metric, window_kind, local_date, rule_ref, overrides_fp)
);
CREATE INDEX resolved_cache_deps_idx ON resolved_cache USING gin (deps);
COMMENT ON TABLE resolved_cache IS 'The results of one local date''s windows of a metric under one rule version and set of overrides. Never authoritative.';
COMMENT ON COLUMN resolved_cache.overrides_fp IS 'Hash of the active overrides of the date and its neighbours; empty without any.';
COMMENT ON COLUMN resolved_cache.deps IS 'Catalogue codes and rule metrics the results read (followed leaders, derived source codes, sleep, wear, "workouts"); a change to any of them inside dep_from..dep_to deletes the row.';
COMMENT ON COLUMN resolved_cache.complete_at IS 'When every window of the date had closed; a request whose now is earlier recomputes.';

-- Invalidation runs in the writer's transaction. A dirty mark deletes the rows that read its
-- code on its date (followers and derived codes list their sources in deps, nights their
-- neighbouring dates in dep_from).
-- +goose StatementBegin
CREATE FUNCTION resolved_cache_on_dirty() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
  DELETE FROM resolved_cache c
  USING (SELECT n.user_id, mc.code, range_agg(daterange(n.local_date, n.local_date, '[]')) AS dates
         FROM dirty_rows n JOIN metric_catalog mc ON mc.id = n.metric_id
         GROUP BY n.user_id, mc.code) m
  WHERE c.user_id = m.user_id AND c.deps @> ARRAY[m.code] AND daterange(c.dep_from, c.dep_to, '[]') && m.dates;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER resolved_cache_dirty_insert AFTER INSERT ON resolution_dirty
  REFERENCING NEW TABLE AS dirty_rows FOR EACH STATEMENT EXECUTE FUNCTION resolved_cache_on_dirty();
CREATE TRIGGER resolved_cache_dirty_update AFTER UPDATE ON resolution_dirty
  REFERENCING NEW TABLE AS dirty_rows FOR EACH STATEMENT EXECUTE FUNCTION resolved_cache_on_dirty();

-- Rule activation: every row that read the metric's rule (followers included).
-- +goose StatementBegin
CREATE FUNCTION resolved_cache_on_rule() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
  IF TG_OP <> 'INSERT' THEN
    DELETE FROM resolved_cache WHERE user_id = OLD.user_id AND deps @> ARRAY[OLD.metric];
  END IF;
  IF TG_OP <> 'DELETE' THEN
    DELETE FROM resolved_cache WHERE user_id = NEW.user_id AND deps @> ARRAY[NEW.metric];
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER resolved_cache_rule AFTER INSERT OR UPDATE OR DELETE ON active_rules
  FOR EACH ROW EXECUTE FUNCTION resolved_cache_on_rule();

-- Workouts have no catalogue metric and mark nothing dirty; rules with a workout context list
-- "workouts" in deps.
-- +goose StatementBegin
CREATE FUNCTION resolved_cache_on_workout() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
  IF TG_OP <> 'INSERT' THEN
    DELETE FROM resolved_cache WHERE user_id = OLD.user_id AND deps @> ARRAY['workouts']
      AND OLD.local_date BETWEEN dep_from AND dep_to;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    DELETE FROM resolved_cache WHERE user_id = NEW.user_id AND deps @> ARRAY['workouts']
      AND NEW.local_date BETWEEN dep_from AND dep_to;
  END IF;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER resolved_cache_workout AFTER INSERT OR UPDATE OR DELETE ON workouts
  FOR EACH ROW EXECUTE FUNCTION resolved_cache_on_workout();

-- Changes that move window bounds or selector matches of rows that are not rewritten (timezone
-- periods, a device's type or model, an origin's name) clear the owner's cache.
-- +goose StatementBegin
CREATE FUNCTION resolved_cache_clear_user() RETURNS trigger LANGUAGE plpgsql SET search_path FROM CURRENT AS $$
BEGIN
  DELETE FROM resolved_cache WHERE user_id = CASE WHEN TG_OP = 'DELETE' THEN OLD.user_id ELSE NEW.user_id END;
  RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER resolved_cache_timezone AFTER INSERT OR UPDATE OR DELETE ON timezone_periods
  FOR EACH ROW EXECUTE FUNCTION resolved_cache_clear_user();
CREATE TRIGGER resolved_cache_device AFTER UPDATE ON devices FOR EACH ROW
  WHEN (OLD.device_type IS DISTINCT FROM NEW.device_type OR OLD.model IS DISTINCT FROM NEW.model)
  EXECUTE FUNCTION resolved_cache_clear_user();
CREATE TRIGGER resolved_cache_origin AFTER UPDATE ON data_origins FOR EACH ROW
  WHEN (OLD.name IS DISTINCT FROM NEW.name OR OLD.relayed_provider_id IS DISTINCT FROM NEW.relayed_provider_id)
  EXECUTE FUNCTION resolved_cache_clear_user();

-- +goose Down
DROP TRIGGER resolved_cache_origin ON data_origins;
DROP TRIGGER resolved_cache_device ON devices;
DROP TRIGGER resolved_cache_timezone ON timezone_periods;
DROP TRIGGER resolved_cache_workout ON workouts;
DROP TRIGGER resolved_cache_rule ON active_rules;
DROP TRIGGER resolved_cache_dirty_update ON resolution_dirty;
DROP TRIGGER resolved_cache_dirty_insert ON resolution_dirty;
DROP FUNCTION resolved_cache_clear_user(), resolved_cache_on_workout(), resolved_cache_on_rule(), resolved_cache_on_dirty();
DROP TABLE resolved_cache, source_hourly_aggregates;
