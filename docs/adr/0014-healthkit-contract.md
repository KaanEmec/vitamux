# ADR-0014 HealthKit device contract: frozen v1 payload, raw units, commit-after-ack, one `health_events` table

Status: Accepted · Date: 2026-10-04 · Deciders: owner

## Context
The Apple Health app and the Go normalizer are built in parallel by [E15](../plan/E15-apple-health/README.md) and must agree on bytes, units and retry behaviour before either ships. Platform facts behind this are verified in [apple-health.md](../architecture/apple-health.md#permissions) ([J15.1](../plan/E15-apple-health/J15.1-platform-contract.md)). Category-type events (alerts, symptoms, mindful sessions) had no table ([metric-catalog › Events](../architecture/metric-catalog.md#events)).

## Decision
- **Payload.** Stream `healthkit.samples.v1`, one item per type page, described by [`schemas/healthkit-samples.v1.json`](../../schemas/healthkit-samples.v1.json), **frozen as v1**: only additive, optional fields. A breaking change is a new stream `healthkit.samples.v2` and a new ADR. See [apple-health.md › Payload](../architecture/apple-health.md#payload).
- **Type registry v1** is `Registry.v1` in [`apple/HealthBridgeKit/Sources/HealthBridgeHealthKit/Registry.swift`](../../apple/HealthBridgeKit/Sources/HealthBridgeHealthKit/Registry.swift), the `E15` and `v1` rows of [metric-catalog](../architecture/metric-catalog.md) that have an HK id. It only grows. An identifier the phone's iOS does not know is skipped, and the normalizer ignores a type it has no mapping for (kept raw, reprocessable).
- **Units are sent raw.** Each quantity is read in the registry's `unit` and sent with that string. Percent types are fractions (`0.97`, as HealthKit reports them) and glucose is `mg/dL`. The phone never converts; the Go normalizer owns conversion to canonical units ([ADR-0016](0016-canonical-writer.md)), so a conversion fix is a reprocess, not an app release. Category values are sent as the raw integer.
- **Idempotency and anchors.** Batch key = `sha256(device|type|anchor_before|first_uuid|last_uuid|count)`; item `external_key` = `<type>:<key>`. The app persists the new anchor **only after `202`**. On any failure it retries with the old anchor and so re-sends the same batch. `409` (same key, body re-read later with a new `fetched_at`) counts as accepted. Records are deduplicated by HealthKit sample UUID in the canonical writer, so overlapping batches and anchor resets are harmless ([sync algorithm](../architecture/apple-health.md#sync-algorithm)).
- **Event storage: DECIDED, one `health_events` table** for HealthKit category events, not one table per family ([`00024_healthkit.sql`](../../internal/db/migrations/00024_healthkit.sql)). A row has `code`, `start_at`, `end_at`, `local_date`, an optional numeric `value`, an optional code-owned `level` word and `context` jsonb, plus the same provenance, dedupe, `superseded_by`, `deleted_at` and raw-payload columns as other canonical tables ([data-model](../architecture/data-model.md#principles)). Event codes are owned by the catalogue code, not free text. Events are not resolvable metrics: they appear in the all-sources view and have no rules.

## Alternatives considered
- **Convert units on the phone** — one less server step, but a wrong conversion ships in an app binary and cannot be reprocessed from raw.
- **Persist the anchor before upload** — simpler, but a crash or failed upload then loses a page of samples permanently.
- **Treat `409` as an error** — the retry loop would never end when the same page is re-read with a new timestamp.
- **One table per event family** — about ten tables and migrations for rows with the same shape and no per-family queries.
- **Store events in `measurements` with a value** — loses labels and context, and would make a symptom or alert look resolvable.
- **Unversioned payload** — the app and server are released separately, so a silent change would corrupt data.

## Consequences
- Contributors add optional fields to the schema, never change or remove one.
- Percent and unit handling is server-side only and covered by normalizer tests with synthetic fixtures.
- The metric-catalog event question is closed; `data-model.md` lists `health_events` with the job that wires the normalizer.
