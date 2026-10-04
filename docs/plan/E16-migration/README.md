# E16 Reference installation and existing-data migration (final epic)

Release: deployment · Depends on: G7 (v0.4.0) · [Plan index](../README.md)
Read first: [migration.md](../../architecture/migration.md) and the owner-local, git-ignored `docs/architecture/migration-reference.md`. **Do not read these for any other epic.**

Status: **not planned in detail.** Job files are written when this epic starts. No product epic may follow E16.

**Objective:** Install the released product on the reference VPS through generic artifacts, enable the Garmin ([E18](../E18-garmin/README.md)) and WHOOP ([E19](../E19-whoop/README.md)) sidecars, and migrate existing data without duplicates. Retire Open Wearables only after explicit owner approval.

## Planned jobs (to be detailed)

| Job | Title |
| --- | --- |
| J16.1 | Read-only inventory (Open Wearables API types and ranges, Garmin archive manifest, WHOOP archive check) |
| J16.2 | Pre-migration backups and restore test |
| J16.3 | Reference VPS installation with the generic Coolify guide (J14.2, J17.4) |
| J16.5 | Enable the E18 Garmin sidecar; retire or adapt the existing collector (push its raw archive through E18's normalizers) |
| J16.6 | Enable the E19 WHOOP sidecar on the reference install |
| J16.7 | Garmin archive importer |
| J16.8 | Open Wearables API importer (complementary) |
| J16.9 | Garmin cross-source dedupe and reconciliation report |
| J16.10 | WHOOP archive import (conditional) |
| J16.11 | Dual running, parity checks, resource validation (gate: owner sign-off) |
| J16.12 | Rollback drill, cutover, delayed retirement of Open Wearables (gate: explicit owner approval) |
