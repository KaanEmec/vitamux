# Development

## Prerequisites

- Go (version pinned in `go.mod`), Node 24+ (SvelteKit 3 needs ≥ 22.17), `golangci-lint` v2
- A container runtime with `docker compose`: Docker Desktop, OrbStack, or colima (`brew install colima docker docker-compose && colima start`)

## Commands

| Command | What it does |
| --- | --- |
| `cp .env.example .env` | Development config (synthetic values only) |
| `make dev` | PostgreSQL in Compose + `vitamux serve` with live reload (`go tool air`) + Vite on http://127.0.0.1:5173, which proxies `/api` and `/healthz` to :8080 |
| `make test` / `make test-integration` | Unit tests (offline) / integration tests against dev PostgreSQL (`-tags integration`) |
| `make lint` | golangci-lint, svelte-check, ESLint |
| `make build` | `bin/vitamux` with the UI embedded (`-tags webui`) |
| `make image` | Release container image (distroless, non-root) |

Without `-tags webui` the binary serves a placeholder page, so backend work never needs Node.

## Database and migrations

- `deploy/sql/roles.sql` (idempotent, run by `make services`) creates the `vitamux` schema and the roles `vitamux_owner` (DDL, used by `vitamux migrate`) and `vitamux_app` (DML only, used by `serve`). Every session runs `SET ROLE` to its role, so a development superuser login still exercises least privilege.
- Migrations are goose SQL files in `internal/db/migrations/NNNNN_name.sql`, embedded in the binary. The newest file number is the schema version `serve` requires; it refuses an older or newer database.
- `vitamux migrate up|status`; `down-to N` only with `VITAMUX_ENV=development`. `VITAMUX_MIGRATE_DATABASE_URL[_FILE]` overrides the URL for migrations.
- Tables get app DML by default privileges. Narrow per table in the same migration (e.g. `REVOKE UPDATE, DELETE ON audit_events FROM vitamux_app`).
- Expand/contract: a release only adds (nullable or defaulted columns, new tables, `CREATE INDEX CONCURRENTLY` in its own `-- +goose NO TRANSACTION` file). Code stops using a column one release before a later migration drops it. Never edit a released migration.
- Integration tests use `internal/db/dbtest`: `dbtest.Migrated(t)` creates a fresh migrated database per test and returns an app-role pool.

## Package boundaries

These rules are enforced by `depguard` in `.golangci.yml`:

| Package | May not import |
| --- | --- |
| everything | std `log` (use `log/slog`) |
| everything except `internal/db` | `github.com/jackc/pgx` |
| `connectors`, `normalize`, `resolve`, `ingest`, `jobs`, `documents`, `imports` | `internal/api` |
| `connectors` | `internal/resolve` |
| `resolve` | `internal/connectors`, `internal/ingest` |
| `normalize` | `internal/connectors` (connectors import normalize, never the reverse) |

## Troubleshooting

- `docker compose` not found with Homebrew's docker: add `"cliPluginsExtraDirs": ["/opt/homebrew/lib/docker/cli-plugins"]` to `~/.docker/config.json`.
- Port 5432 already in use: set `VITAMUX_DEV_PG_PORT` in `.env` and adjust `VITAMUX_DATABASE_URL`.
