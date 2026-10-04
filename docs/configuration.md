# Configuration reference

Every `VITAMUX_*` variable the binary reads, from [`internal/config`](../internal/config/config.go). `TestConfigurationReference` (`internal/config/reference_test.go`) fails when a variable, default or secret flag here differs from the code; descriptions are hand-written.

- **Secret** rows are read from `<NAME>_FILE`, a path to a file holding the value (trailing newline ignored). The plain `<NAME>` is accepted only with `VITAMUX_ENV=development`; setting both is an error.
- Empty means unset. Invalid values stop `serve` with every problem listed at once.
- The container image sets `VITAMUX_HTTP_ADDR=:8080` and `VITAMUX_DATA_DIR=/data`.
- Compose-only settings (`VITAMUX_IMAGE`, `VITAMUX_PORT`) are interpolated by Compose, not read by the binary: see [`deploy/compose/.env.example`](../deploy/compose/.env.example).

## Core

| Variable | Default | Secret | Since | Description |
| --- | --- | --- | --- | --- |
| `VITAMUX_ENV` | `production` |  | 0.1.0 | `production` or `development`. Development allows plain secret values, `migrate down-to`, and plain-http cookies. |
| `VITAMUX_HTTP_ADDR` | `127.0.0.1:8080` |  | 0.1.0 | Listen address of the UI, API, OAuth callbacks and webhooks. Also probed by `vitamux healthcheck`. |
| `VITAMUX_PUBLIC_URL` | `http://127.0.0.1:8080` |  | 0.1.0 | The URL users and providers reach. Base of OAuth callbacks and webhook URLs. Must be https in production unless it is a loopback address. |
| `VITAMUX_TRUSTED_PROXIES` |  |  | 0.1.0 | Comma-separated CIDRs or addresses whose `X-Forwarded-For`/`-Proto` are believed. Empty trusts none. |
| `VITAMUX_LOG_LEVEL` | `info` |  | 0.1.0 | `debug`, `info`, `warn` or `error`. Logs are JSON on stderr and never contain secrets or bodies. |
| `VITAMUX_METRICS_ADDR` |  |  | 0.1.0 | Private `host:port` for `/metrics` (e.g. `127.0.0.1:9090`). Empty disables it; it must differ from `VITAMUX_HTTP_ADDR`. |
| `VITAMUX_DATA_DIR` | `./data` |  | 0.1.0 | Blob store and documents (`<dir>/blobs`). Must be writable; `/readyz` checks it. |

## Database

| Variable | Default | Secret | Since | Description |
| --- | --- | --- | --- | --- |
| `VITAMUX_DATABASE_URL` |  | yes | 0.1.0 | PostgreSQL URL of the DML-only `vitamux_app` role, used by `serve` and most commands. |
| `VITAMUX_MIGRATE_DATABASE_URL` |  | yes | 0.1.0 | URL of the DDL `vitamux_owner` role for `migrate` and `restore`. Falls back to `VITAMUX_DATABASE_URL`. |

## Keys and backups

| Variable | Default | Secret | Since | Description |
| --- | --- | --- | --- | --- |
| `VITAMUX_MASTER_KEY_FILE` |  |  | 0.1.0 | The master key file (64 hex characters, mode 0600), created by `vitamux admin init-secrets`. Without it sign-in, syncs and documents are unavailable. |
| `VITAMUX_PREVIOUS_MASTER_KEY_FILES` |  |  | 0.1.0 | Comma-separated retired key files that still open old values during a [key rotation](operations/key-rotation.md). |
| `VITAMUX_BACKUP_DIR` |  |  | 0.1.0 | Enables the daily backup job and is the default `vitamux backup --out` ([backup](operations/backup.md)). |
| `VITAMUX_BACKUP_KEEP` | `3` |  | 0.1.0 | How many scheduled backups to keep (positive integer). |

## Providers

| Variable | Default | Secret | Since | Description |
| --- | --- | --- | --- | --- |
| `VITAMUX_WITHINGS_CLIENT_ID` |  |  | 0.1.0 | Client id of your own Withings application ([withings](providers/withings.md#app-registration-and-callback)). Set together with the secret, or neither. |
| `VITAMUX_WITHINGS_CLIENT_SECRET` |  | yes | 0.1.0 | Client secret of that application. |
| `VITAMUX_SIDECARS` |  |  | 0.2.0 | Remote sidecar connectors, `name=url[,name=url]` ([sidecars](sidecars.md)). `name` is the provider code the sidecar describes; the URL must resolve to a loopback, private or link-local address (checked on every connection). An unreachable sidecar never blocks startup. |
| `VITAMUX_SIDECAR_<NAME>_SECRET` |  | yes | 0.2.0 | Bearer secret shared with sidecar `<name>` (upper case in the variable). Default file `<data dir>/secrets/sidecar-<name>.secret`; `vitamux admin init-secrets` creates missing ones. |

## Lab extraction providers

Optional; each is off until configured here and enabled by the owner ([privacy controls](architecture/lab-documents.md#privacy-controls)).

| Variable | Default | Secret | Since | Description |
| --- | --- | --- | --- | --- |
| `VITAMUX_GEMINI_API_KEY` |  | yes | 0.1.0 | Google Gemini API key. Set together with `VITAMUX_GEMINI_MODEL`. |
| `VITAMUX_GEMINI_MODEL` |  |  | 0.1.0 | Gemini model id that extraction consent names. |
| `VITAMUX_OPENAI_API_KEY` |  | yes | 0.1.0 | OpenAI API key. Set together with `VITAMUX_OPENAI_MODEL`. |
| `VITAMUX_OPENAI_MODEL` |  |  | 0.1.0 | OpenAI model id that extraction consent names. |
| `VITAMUX_OPENAI_COMPATIBLE_BASE_URL` |  |  | 0.1.0 | Base URL of a self-hosted server implementing the Responses API with file input. https on a public host, without credentials, query or fragment. Set together with the model. |
| `VITAMUX_OPENAI_COMPATIBLE_MODEL` |  |  | 0.1.0 | Model id on that server. |
| `VITAMUX_OPENAI_COMPATIBLE_API_KEY` |  | yes | 0.1.0 | Optional key for that server. |
| `VITAMUX_OPENAI_COMPATIBLE_ALLOW_PRIVATE` |  |  | 0.1.0 | `true` allows http and private or loopback hosts in the base URL (local inference). |
