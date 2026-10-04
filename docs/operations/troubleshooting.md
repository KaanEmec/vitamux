# Troubleshooting

Symptoms an operator meets, what they mean and what to do. Commands assume the Compose install in `deploy/compose/` ([install](../install.md)); without Compose run the same `vitamux` subcommands with the same environment. Logs never contain secrets or health values, so they are safe to paste into an issue.

## `/readyz` answers 503

`/readyz` names the failing checks only, e.g. `{"status": "unavailable", "checks": {"database": "ok", "master_key": "fail", ...}}`. The reason is in the log line `readiness check failed` with the check name: `docker compose logs vitamux | grep readiness`.

| Check | Usual cause | Fix |
| --- | --- | --- |
| `database` | PostgreSQL down or the app role's password does not match `secrets/database_url` | `docker compose ps postgres`; after changing a role password, update its secret file too |
| `schema` | The database is at another schema version than the binary | See [schema version mismatch](#schema-version-mismatch) |
| `data_dir` | `VITAMUX_DATA_DIR` missing or not writable by uid 65532 | Named volumes get the right owner; for a bind mount `chown 65532:65532` it |
| `master_key` | `VITAMUX_MASTER_KEY_FILE` unset, missing, not 64 hex characters, or readable by group or others | Create it once with `init-secrets` ([install](../install.md#3-start-the-stack)); `chmod 600`; never generate a new one over lost data |
| `workers` | The job runner has not run for 60 s: the process is shutting down, or the database is unreachable | Check `database` first; restart `vitamux` |

The container healthcheck (`vitamux healthcheck`) probes the same endpoint, so `docker compose ps` shows `unhealthy` for any of these.

## Schema version mismatch

`serve` exits at start (and `/readyz` reports `schema`) with one of:

- `database schema is at version N, this binary needs M: run vitamux migrate up`: the migration step did not run or failed. `docker compose logs migrate`, then `docker compose up -d --wait` again.
- `database schema is at version N, newer than this binary (M): upgrade vitamux`: an older image is running against an upgraded database, e.g. after a rollback. Run the newer image, or follow [upgrade#rollback](upgrade.md#rollback).

`docker compose run --rm migrate migrate status` lists applied and pending migrations.

## Source setup

Connections › **Connect a source** shows each provider's setup state ([ADR-0021](../adr/0021-source-setup.md)) and explains a refusal in plain words. The usual causes:

| The panel says | Cause | Fix |
| --- | --- | --- |
| Needs an https public address | `VITAMUX_PUBLIC_URL` is http, an IP or localhost, uses another port than 443, or makes the callback longer than 255 characters | Set it to the https domain of your proxy and restart |
| Refused the client id and secret | Withings rejected them at **Save and check** | Copy both again from the Withings developer dashboard |
| Withings shows a redirect URI error | The callback URL registered in the Withings app differs from the panel's | Copy the callback URL from step 1 of the wizard into the Withings app |
| Connecting Withings failed … check the client id, the secret and the callback URL | The exchange after the Withings sign-in was refused (`auth_error=exchange_failed`) | **Review the app setup**, or replace the credentials under Settings › Sources |
| Not available: its sidecar is not running | The bundled sidecar is off, or its secret file is missing | Add the line the card shows ([install#sidecars](../install.md#sidecars)), apply it, then **Check again** |
| Did not accept the email or password / the verification code | Wrong password, or a wrong or expired code | Start again; use the newest code |
| Is limiting sign-in attempts | The provider rate-limits sign-ins | Wait the time shown, then start again |

## A connection says `needs_reauth`

The provider refused the stored credentials: the refresh token was revoked (app removed in the provider account, password change), expired, or rotated without being saved. Syncs and schedules stop; nothing is retried.

Fix: open the connection and choose **Reauthorize** on its Overview tab (it calls `POST /api/v1/connections/{id}/auth/begin`) and sign in to the **same** provider account; another account is refused with `account_mismatch`. Data, cursors and schedules are kept, and a sync is queued right away. If it comes back soon after, check that the Withings app credentials (Settings › Sources, or `VITAMUX_WITHINGS_CLIENT_ID` and the secret file) still match the provider application, and that the host clock is right.

## A stream is `degraded` (`schema_drift`)

The provider answered with a shape the connector does not know. Vitamux stored that page as `quarantined` raw data, did not move the cursor and did not substitute anything ([typed errors](../architecture/connectors.md#typed-errors)). The connection page shows the stream as degraded; the next scheduled run tries again, and the stream returns to `ok` by itself if the provider's answer is back to normal.

If it persists, the provider changed its API: upgrade Vitamux, or open an issue with the provider, stream and the run's error message, which names the endpoint and the shape fingerprint (the connection's run history, `GET /api/v1/connections/{id}/runs`). Never attach the payload. Quarantined raw rows stay stored for reprocessing once a fixed normalizer ships.

A connection that stays `degraded` for a stream its connector no longer declares (for example after a sidecar update) clears by itself: the stream is retired when the sidecar is described again, or on the next sync that names it. A degraded stream whose schedules you disabled does not count either.

## Other connection health words

| Health | Meaning | What to do |
| --- | --- | --- |
| `degraded` / `rate_limited` | The provider asked Vitamux to wait (`Retry-After`); calls resume by themselves | Nothing; avoid manual syncs in a loop |
| `degraded` / `recent_failures` | A transient failure (network, provider 5xx); retried with backoff | Nothing unless it becomes `failing` |
| `failing` | Three or more failures in a row, or a permanent error (`permanent`: the connection is in `error` and its schedules stop) | Read the error class on the connection page and the log; after fixing the cause, resume the connection |
| `stale` | No failure, but no success for three schedule intervals | Check that `vitamux` runs (`workers` in `/readyz`) and that the connection is not blocked by a long `Retry-After` |
| `ok` / `awaiting_first_sync` | Connected; the first sync has not finished yet | Wait; a large history takes a while (Withings sends everything on the first run) |

The definitions are in `connectors.DeriveHealth` ([`internal/connectors/health.go`](../../internal/connectors/health.go)).

## Resolved values look wrong after an upgrade

The resolved cache is checked against live resolution with:

```sh
docker compose exec vitamux /vitamux resolve verify --windows 500
```

It exits 1 and lists every (metric, date) pair whose cached result differs. That is a bug: please report the list (codes and dates only). Meanwhile clearing the cache is always safe; it refills on the next requests:

```sh
docker compose exec postgres psql -U vitamux_admin -d vitamux -c 'TRUNCATE vitamux.resolved_cache'
```

Run `resolve verify` after every [restore](backup.md#restore) too. If the values are right but unexpected, open the metric's drilldown in the UI: every resolved value lists its inputs, rule version and explanation.

## Sign-in answers 503

The master key did not load (`master_key` in `/readyz`). Sign-in, provider syncs and documents need it.
