# Upgrade

How to move a Docker Compose install to a new Vitamux release. The design is in [reliability#upgrades](../architecture/reliability.md#upgrades); migration rules for contributors are in [development#database-and-migrations](../development.md#database-and-migrations).

## Steps

1. Read the [changelog](../../CHANGELOG.md), especially the breaking changes and upgrade notes of every version you cross.
2. Take a backup and copy it off the host ([backup](backup.md)):

   ```sh
   docker compose exec vitamux /vitamux backup
   ```

3. Set the new tag in `.env` (`VITAMUX_IMAGE=ghcr.io/kaanemec/vitamux:vX.Y.Z`; pin tags, not `latest`), then:

   ```sh
   docker compose pull
   docker compose up -d --wait
   ```

Compose follows the dependency order. The one-shot `migrate` runs `vitamux migrate up` with the new image as the DDL owner role while the old `vitamux` keeps serving (migrations only expand the schema within a release). Then Compose replaces `vitamux`: the old process gets `SIGTERM`, stops claiming jobs, lets running ones finish or checkpoint for up to 50 s and releases the rest; `stop_grace_period: 60s` gives it that time before a kill. The new process starts only if `migrate` succeeded, checks the schema version, and resumes syncs from their cursors and backfill units, so an interrupted job repeats at most one page.

Check the result: `docker compose ps` shows `vitamux` healthy, and `docker compose run --rm vitamux version` prints the version and the schema number the binary expects. If `migrate` failed, `docker compose logs migrate` names the migration; a failed migration is rolled back (each runs in a transaction unless it is marked `NO TRANSACTION`), and `vitamux` keeps refusing to start until `migrate` succeeds.

Without Compose the order is the same: stop `serve` (send `SIGTERM`, wait up to 60 s), run `vitamux migrate up` with `VITAMUX_MIGRATE_DATABASE_URL_FILE`, start `vitamux serve`.

## Rollback

- `serve` refuses a database whose schema is newer than the binary, and production has no down migrations. So an older image cannot run after a release that added a migration.
- If the release added no migration (same schema number in `vitamux version`), set the old tag again and `docker compose up -d --wait`.
- Otherwise roll back by restoring the backup from step 2 into a fresh stack with the old image ([backup#restore](backup.md#restore)). Data written since that backup is lost, except what providers still hold: the next syncs fetch it again. Prefer a fix-forward release when the problem is not data-threatening.
- Never edit `goose_db_version` or run SQL by hand to "downgrade".

## Coolify

Change the image tag in the Coolify resource and redeploy. The same order applies: the one-shot services and `migrate` run before `vitamux` starts ([install#coolify](../install.md#coolify)).
