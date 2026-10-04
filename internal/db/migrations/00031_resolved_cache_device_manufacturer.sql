-- Brand selectors (J09.11; docs/architecture/resolution.md#selectors-and-validation) match devices.manufacturer,
-- so a manufacturer change clears the owner's resolved cache like a type or model change (00022).

-- +goose Up
DROP TRIGGER resolved_cache_device ON devices;
CREATE TRIGGER resolved_cache_device AFTER UPDATE ON devices FOR EACH ROW
  WHEN (OLD.device_type IS DISTINCT FROM NEW.device_type OR OLD.model IS DISTINCT FROM NEW.model
    OR OLD.manufacturer IS DISTINCT FROM NEW.manufacturer)
  EXECUTE FUNCTION resolved_cache_clear_user();

-- +goose Down
DROP TRIGGER resolved_cache_device ON devices;
CREATE TRIGGER resolved_cache_device AFTER UPDATE ON devices FOR EACH ROW
  WHEN (OLD.device_type IS DISTINCT FROM NEW.device_type OR OLD.model IS DISTINCT FROM NEW.model)
  EXECUTE FUNCTION resolved_cache_clear_user();
