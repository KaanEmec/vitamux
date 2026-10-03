# Migration and reference installation (deferred)

Status: **not designed in detail yet.** This covers epic E16, the last epic. Do not read or design for the reference VPS, Open Wearables, the existing Garmin collector, or WHOOP/Dofek while working on E01–E15.

Reference facts (VPS, Coolify, Open Wearables, Garmin collector and archive, WHOOP plans, current Withings callback) are in [migration-reference.md](migration-reference.md).

## Scope to plan later

- Install released artifacts on the reference VPS with the generic Coolify guide (J14.2); only deployment-time values differ.
- **Garmin collector**: adapt the existing Python collector so it submits raw archived responses through push ingest ([connectors.md](connectors.md#push-ingest-contract)), and add Go Garmin normalizers.
- **WHOOP**: a sidecar using `@dofek/whoop` via the remote sidecar mode ([connectors.md](connectors.md#remote-sidecar-mode), built in E17), with Go normalizers. Fact checked on 2026-10-03: v0.1.63, MIT, ESM, Node ≥ 22.14, depends on `@dofek/provider-http` and `@dofek/training`.
- Importers:
  - Garmin collector archive (preferred source);
  - Open Wearables API (`filter_by_priority=false`, cursor paging) as a complement;
  - any WHOOP archives.
- Garmin dedupe and reconciliation into categories: archive-only, OW-only, matching, conflicting.
- Backups, dual running, parity checks, cutover, rollback, and delayed retirement of Open Wearables after explicit owner approval.

## What the core already provides for this

- Account-scoped dedupe keys, so imports and live sync collide correctly ([data-model.md](data-model.md#identifiers-and-dedupe-keys)).
- `ingest_batches.source_kind = migration` + `migration_source`, recording migration provenance separately from provider provenance.
- `import_runs` and `import_items` for restartable, idempotent importers.
- The `migrated_without_raw` quality flag; raw-first storage of every imported file or API page.
