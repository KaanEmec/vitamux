# Backup and restore

How to back up and restore a Vitamux instance. The design is in [reliability#backup-and-restore](../architecture/reliability.md#backup-and-restore). A backup holds the whole instance for disaster recovery. An [export](../architecture/api.md#exports) is portable owner data and does not replace a backup.

## What a backup is

`vitamux backup` writes a directory `vitamux-<UTC time>/` (mode 0700, files 0600) with:

| File | Content |
| --- | --- |
| `db.dump` | `pg_dump --format=custom` of the `vitamux` schema, taken as `vitamux_app` (read-only access is enough) |
| `blobs.tar` | the blob store under `<VITAMUX_DATA_DIR>/blobs`, including `names.key` ([ADR-0004](../adr/0004-blob-store.md)); `tmp/` is left out |
| `manifest.json` | format, `created_at`, Vitamux and schema version, the master `key_id`, blob count, size and SHA-256 of each file, and a MAC |

The MAC is the manifest's SHA-256 sealed with the master key, so restore rejects a manifest or file that was edited. **The master key is not in the backup.** Without it, the backup cannot be verified or restored, and provider tokens and sealed documents are lost. Keep the key file (`/secrets/master.key` in the `vitamux-secrets` volume) and `deploy/compose/secrets/` somewhere other than the backups ([security#keys-and-secrets](../architecture/security.md#keys-and-secrets)).

The image ships `pg_dump`/`pg_restore` 18, which work with PostgreSQL 18 servers (the reference stack) and can dump older ones. Outside the image, install the client tools that match the server's major version.

## Scheduled backups

`serve` runs a daily `backup` job when `VITAMUX_BACKUP_DIR` is set and keeps the newest `VITAMUX_BACKUP_KEEP` backups (default 3). The release Compose file enables it, writing to the `vitamux-backups` volume. Runs appear in the job history under kind `backup`. Each backup is a full copy, so plan for about 2–3× the data size ([project#resource-budget](../architecture/project.md#resource-budget)).

Manual run: `docker compose exec vitamux /vitamux backup` (default directory) or `--out DIR`. `--out -` writes a tar to stdout. It is built in `TMPDIR` first, so point `TMPDIR` at a volume with room for the whole backup; the container's `/tmp` is only 64 MB.

## Off-host and encrypted copies

The volume sits on the same disk as the data. Copy each backup off the host, encrypted. Vitamux has no built-in encryption. To read backups from the host, replace the `vitamux-backups` volume with a bind mount (`chown 65532:65532` it), then use for example:

```sh
restic -r sftp:backup-host:/vitamux backup /srv/vitamux/backups          # encrypted, deduplicated
tar -C /srv/vitamux/backups -c vitamux-20261003T030000Z | age -r age1… > vitamux-20261003.tar.age
rclone sync /srv/vitamux/backups crypt-remote:vitamux                    # with an rclone crypt remote
```

Backups contain health data, sealed provider tokens and `names.key`. Treat them like the database.

## Restore

Restore needs an **empty** target: a database with [`roles.sql`](../../deploy/sql/roles.sql) applied but no migrations, and an empty data directory. It also needs the master key the manifest names (plus previous keys if a rotation was unfinished, through `VITAMUX_PREVIOUS_MASTER_KEY_FILES`). On a fresh Compose stack, follow [compose.md#install](../deploy/compose.md#install) up to `./init-secrets.sh`, then put the old master key back instead of running the `init-secrets` service:

```sh
docker run --rm -v vitamux_vitamux-secrets:/secrets -v "$PWD/master.key:/in:ro" busybox \
  sh -c 'cp /in /secrets/master.key && chown 65532:65532 /secrets/master.key && chmod 600 /secrets/master.key'
docker compose up -d --wait postgres                 # creates roles and schema, no migrations yet
chown -R 65532:65532 /srv/restore                    # the backup copied back from off-host
docker compose --profile restore run --rm -v /srv/restore:/restore:ro restore --from /restore/vitamux-20261003T030000Z
docker compose up -d --wait                          # migrate (nothing to do) and vitamux
```

`vitamux restore --from DIR` runs as the owner role (`VITAMUX_MIGRATE_DATABASE_URL`) and, in order:

1. Verifies the manifest MAC and every file's size and checksum. On any mismatch it stops before writing anything.
2. Refuses a backup from a newer schema, a non-empty database, or a non-empty blob directory.
3. Unpacks the blobs and opens `names.key` with the master key.
4. Runs `pg_restore` in one transaction. The app role's default privileges are lifted meanwhile, so the narrowed table privileges come back exactly.
5. Checks that every blob with references has a file.
6. Runs `migrate up` if the backup is from an older schema.

## Restore drill

`TestRestoreDrill` (`internal/backup`, integration tag) runs in CI against PostgreSQL 17 and 18. It generates a fixturegen slice, backs it up, rejects a tampered copy, restores into a fresh database and data directory, and then compares row counts, privileges, blob files and resolved results. Locally it needs `pg_dump`/`pg_restore` of the dev server's major version on `PATH`. Otherwise it is skipped:

```sh
set -a; . ./.env; set +a; go test -tags integration -run TestRestoreDrill ./internal/backup/
```
