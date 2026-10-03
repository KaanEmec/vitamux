# ADR-0016 Canonical writer: account-scoped dedupe keys, corrections supersede

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Raw payloads are replayed, re-fetched in correction windows, reprocessed after a normalizer bump, and imported from exports, yet each upstream record must exist once with its history ([data-model.md › Principles](../architecture/data-model.md#principles), [Identifiers and dedupe keys](../architecture/data-model.md#identifiers-and-dedupe-keys)). Normalizers are pure ([connectors.md › Normalizer contract](../architecture/connectors.md#normalizer-contract)), so identity and history live in one writer.

## Decision
- One writer, `normalize.Write(ctx, q, Source, Output)`, runs inside the caller's transaction (J07.5 uses one per raw payload). Plain functions over sqlc queries; no repository layer.
- **Dedupe key** = first 16 bytes of SHA-256 over `v1|provider|account_key|record_type|external_id|component`, or the natural key `v1|provider|account_key|metric|kind|start_us|end_us|device|origin` when the source has no stable id. Parts are backslash-escaped for `|` and `\`. `account_key` is hex of `connections.account_key` (never the connection id), so a reconnect or an importer produces the same keys; a connection without a known account falls back to `conn:<id>`. Events use their table (`sleep`, `workout`, the group kind) as metric. Group components without their own id use the group's id with the metric as component.
- **Upsert rule** per row, compared on every content column (not connection or raw id): no active row → insert; equal → untouched, apart from `normalizer_version_id`/`normalized_at` on a version change; different, or deleted and reappearing → supersede the old row, insert, link `superseded_by`. Sleep stages and workout segments are part of their event: any change supersedes the whole event. Measurements are written set-based (one lookup, one insert via `jsonb_to_recordset`) because intraday payloads carry thousands of rows.
- **Tombstones** carry an upstream id and set only `deleted_at` and `deleted_by_raw_id` on the active row in every canonical table; a deleted group deletes its components.
- The writer owns canonical units (`source_value`/`source_unit_id` only when converted), local dates (record offset, record zone, then timezone periods), and the `implausible` and `relayed` quality flags. Devices and origins are upserted per payload; a new origin takes `relayed_provider_id` from `known_relay_origins`.
- Every insert, supersede and delete marks `resolution_dirty(user, metric, local_date)` in the same transaction, for both the old and new date; sleep sessions mark every sleep-derived metric for their `sleep_date`. Workouts have no catalogue metric yet and mark nothing.

## Alternatives considered
- **Connection-scoped keys**: simpler, but a reconnect or an import would duplicate every record.
- **Update in place with an audit table**: smaller tables, but breaks "raw first, history kept" and makes resolution irreproducible.
- **Content hash column for equality**: cheaper comparisons, but one more derived column to keep correct; column comparison is explicit and fast enough per payload.

## Consequences
- Replay, reprocess and reconnect are no-ops on unchanged data; tests pin 3× replay, single supersession, tombstones and reconnects (`writer_integration_test.go`).
- Changing a key format needs `v2` and a re-key migration.
- Natural-keyed records whose time changes upstream appear as a new record; only sources with stable ids get true corrections.
- Two writers racing on the same new key get `db.ErrConflict`; the normalize job retries the payload.
- J09 adds `resolved_cache` invalidation to the same transaction.
