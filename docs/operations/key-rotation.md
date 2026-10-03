# Master key rotation

How to replace the master key without downtime. The format and the reasoning are in [ADR-0011](../adr/0011-secrets-vault.md); what the key protects is in [security#keys-and-secrets](../architecture/security.md#keys-and-secrets). Rotate when the key may have leaked, or on your own schedule.

## What changes

| Sealed with the master key | During rotation |
| --- | --- |
| Provider credentials (`credentials`) | Re-sealed by `vitamux keys rotate` |
| TOTP secrets (`users.totp`) | Re-sealed by `vitamux keys rotate` |
| Per-document data keys (`document_keys`) | Re-sealed by `vitamux keys rotate`. PDFs, file names and extraction responses are sealed with those data keys, so they are not touched. |
| Blob file names key (`<data dir>/blobs/names.key`) | Re-sealed by `serve` the first time it starts with the new key; blob file names never change |
| Session signing (CSRF tokens, OAuth `state`, pagination cursors) | Derived from the current key: tokens issued before the restart stop working. Reload the UI; restart a pending provider authorization. Sessions themselves survive. |
| Backups | A backup's manifest is sealed with the key that was current when it was made. Keep the old key as long as you keep those backups. |

`keys rotate` works in batches of 100 rows per transaction, skips rows another transaction holds, and writes one `keys.rotate` audit event with the counts. It is idempotent and resumable: run it again after an interruption.

## Steps (Docker Compose)

1. Create the new key in the `vitamux-secrets` volume, next to the old one:

   ```sh
   docker compose --profile setup run --rm init-secrets admin init-secrets --out /secrets/master-2.key
   ```

2. Make it current and keep the old one readable, with a `compose.override.yaml` next to `compose.yaml` (Compose merges it automatically):

   ```yaml
   services:
     vitamux:
       environment:
         VITAMUX_MASTER_KEY_FILE: /secrets/master-2.key
         VITAMUX_PREVIOUS_MASTER_KEY_FILES: /secrets/master.key
   ```

   `docker compose up -d --wait`. New values are now sealed with the new key, old ones still open, and `names.key` was re-sealed on start.

3. Re-seal the rest:

   ```sh
   docker compose exec vitamux /vitamux keys rotate
   ```

   It prints the current key id and the counts per table. Run it again until every count is 0.

4. Remove `VITAMUX_PREVIOUS_MASTER_KEY_FILES` from the override (keep the new `VITAMUX_MASTER_KEY_FILE`) and `docker compose up -d --wait`. Copy the new key to where you keep the old one ([backup](backup.md#what-a-backup-is)), and archive the old key until no backup sealed with it remains. The `restore` service needs the same override when it restores a backup made with the new key.

Without Compose: `vitamux admin init-secrets --out NEW`, restart `serve` with `VITAMUX_MASTER_KEY_FILE=NEW` and `VITAMUX_PREVIOUS_MASTER_KEY_FILES=OLD`, run `vitamux keys rotate` with the same environment, then drop the previous setting and restart.

Never delete the old key before step 3 reports 0: values still sealed with it would be lost (provider tokens need a reconnect; documents are unrecoverable).
