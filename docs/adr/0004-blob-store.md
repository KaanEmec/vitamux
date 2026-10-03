# ADR-0004 Blob store: filesystem, content-addressed, zstd

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Raw payloads must be kept verbatim for reprocessing, and lab PDFs and activity files need private storage. Keeping them in PostgreSQL would bloat dumps and vacuum. See [overview › Key decisions](../architecture/overview.md#key-decisions), [connectors › Runtime responsibilities](../architecture/connectors.md#runtime-responsibilities) and [reliability › Backup and restore](../architecture/reliability.md#backup-and-restore).

## Decision
- **Addressing**: a blob is keyed by the SHA-256 of its uncompressed content (`blobs.sha256`, also `raw_payloads.content_sha256`). Same content is stored once.
- **Layout** (`internal/blob`, under `<VITAMUX_DATA_DIR>/blobs`): `<n[0:2]>/<n>` where `n` = hex HMAC-SHA256(names key, sha256), so a listing reveals no content hashes. The names key is 32 random bytes in `names.key`, sealed with purpose `blob-names` ([ADR-0011](0011-secrets-vault.md)); open reseals it after a master key rotation, so names never change. A missing `names.key` next to existing blobs is a hard error. `tmp/` holds in-flight writes.
- **File format**: one format byte, then a zstd stream (`1`), or a sealed value over the zstd bytes (`2`, purpose `documents`, AAD `blob:<hex sha256>`, `blobs.key_id` set). Sealing is a per-call mode (`blob.Sealed`), used for lab PDFs; plain blobs rely on volume encryption like the database.
- **Write**: stream into `tmp/` while hashing and compressing, fsync, then (inside the caller's transaction) take the shared blob lock, insert the `blobs` row, rename into place and fsync the directory. Existing content is not rewritten; a missing file for an existing row is healed.
- **Read**: every read is verified; a hash, zstd or GCM mismatch returns `ErrCorrupt`. Streaming reads verify at EOF.
- **References**: `blobs.refcount` counts referencing rows. `Retain`/`Release` run in the same transaction as the referencing insert/delete. Foreign keys stay as a safety net.
- **Sweep** (`blob.Sweep`, job kind `sweep_blobs`): under the exclusive blob lock (an advisory lock that writers hold shared until commit), delete rows with refcount 0 older than the grace period (default 24 h) and their files, then remove files without a row and stale temp files older than the grace period. The lock means no writer is mid-transaction, so an old file without a committed row is an orphan.

## Alternatives considered
- **`bytea` or large objects in PostgreSQL**: one store, but dumps and restores grow with every payload, and `pg_dump` cannot copy incrementally.
- **Plain hash file names**: simpler, but leak which content is present (e.g. a known PDF).
- **HMAC under the `blob-names` purpose key directly**: no extra file, but every name changes on master key rotation, and the keyring exposes only the current purpose key.
- **Object storage (S3)**: deferred ([project.md](../architecture/project.md)); the package boundary allows a second backend later.
- **Row locks instead of an advisory lock**: cannot protect files whose row does not exist yet.

## Consequences
- Backups copy `names.key` with the blobs; the master key is still needed to open it and sealed blobs.
- Blob writes and the sweep serialize briefly; the sweep holds writers for one directory walk.
- Every table that references blobs must keep `refcount` in step; a drifted count fails the sweep on the foreign key before any file is removed.
- `Sealed` uses the master `documents` key, so it is not crypto-shreddable; per-document data keys ([lab-documents.md](../architecture/lab-documents.md)) stay with J12.1, which can encrypt before `Put`.
- Follow-ups: schedule `sweep_blobs` on the job queue (E06); rewrap sealed blobs during `keys rotate` (J03.1); a restore check that every referenced blob exists (E13).
