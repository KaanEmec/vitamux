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
| `vitamux admin init-secrets` | Creates the master key file (`VITAMUX_MASTER_KEY_FILE`, default `./data/master.key`); never overwrites |
| `vitamux admin create-owner` | Creates the single owner account; username and password from the terminal or two lines on stdin (`reset-password` likewise) |
| `make image` | Release container image (distroless, non-root) |

Without `-tags webui` the binary serves a placeholder page, so backend work never needs Node.

## Database and migrations

- `deploy/sql/roles.sql` (idempotent, run by `make services`) creates the `vitamux` schema and the roles `vitamux_owner` (DDL, used by `vitamux migrate`) and `vitamux_app` (DML only, used by `serve`). Every session runs `SET ROLE` to its role, so a development superuser login still exercises least privilege.
- Migrations are goose SQL files in `internal/db/migrations/NNNNN_name.sql`, embedded in the binary. The newest file number is the schema version `serve` requires; it refuses an older or newer database.
- `vitamux migrate up|status`; `down-to N` only with `VITAMUX_ENV=development`. `VITAMUX_MIGRATE_DATABASE_URL[_FILE]` overrides the URL for migrations.
- Tables get app DML by default privileges. Narrow per table in the same migration (e.g. `REVOKE UPDATE, DELETE ON audit_events FROM vitamux_app`).
- Expand/contract: a release only adds (nullable or defaulted columns, new tables, `CREATE INDEX CONCURRENTLY` in its own `-- +goose NO TRANSACTION` file). Code stops using a column one release before a later migration drops it. Never edit a released migration.
- Integration tests use `internal/db/dbtest`: `dbtest.Migrated(t)` creates a fresh migrated database per test and returns an app-role pool.
- Queries live in `internal/db/queries/*.sql`; `make sqlc` regenerates `internal/db/dbq` (commit the output; CI fails on drift). Domain code uses `db.DB` (`Q()`, `Tx`, `CopyFrom`) and `db.ErrNotFound`/`db.ErrConflict`, never pgx.
- After editing `api/openapi.yaml`, `make openapi` regenerates `internal/api/oapi` and `web/src/lib/api/schema.d.ts` (commit both; CI fails on drift and runs Spectral). See [api.md](architecture/api.md#implementing-owner-endpoints).
- [`docs/schema/`](schema/README.md) is generated from the migrated schema; the integration tests fail when it drifts. After changing a migration, run `VITAMUX_UPDATE_SCHEMA_DOC=1 go test -tags integration -run TestSchemaDoc ./internal/db`.

## Writing integration tests

- Put them in `*_integration_test.go` with `//go:build integration`; `make test-integration` and the CI `integration` job (PostgreSQL 17 and 18) run them. Keep plain unit tests tag-free so `make test` stays offline.
- `dbtest.Migrated(t)` returns a database URL and an app-role pool on a fresh, fully migrated database dropped at test end, so tests may run in parallel. `dbtest.Empty(t)` skips migrations. `dbtest.Truncate(t, url)` empties every app-writable table between scenarios and keeps seeded reference data. Test as the app role; the domain code under test takes `db.New(pool)`.
- `internal/testutil/fakeprovider` replaces provider HTTP. Queue the requests you expect with `Expect(...)` (strict order; method, path, query, form, header and `Check` assertions), each with a scripted reply: `JSON`, `RateLimited` (429 + Retry-After), `ServerError`, `Unauthorized`, `InvalidGrant`, `Malformed`, `Drop`, `Status`, and `TokenRefresh` for a rotating OAuth token. The package comment maps each helper to a [typed error](architecture/connectors.md#typed-errors). Unexpected requests and unconsumed steps fail the test; failure messages name keys, never values.
- Point the code under test at `fake.URL`. See `internal/testutil/fakeprovider/example_integration_test.go`.
- Use synthetic values only (`synthetic-access-1`). Fixtures follow the [synthetic fixtures policy](architecture/project.md#synthetic-fixtures-policy); `make fixture-guard` checks them.

## Package boundaries

These rules are enforced by `depguard` in `.golangci.yml`:

| Package | May not import |
| --- | --- |
| everything | std `log` (use `log/slog`) |
| everything except `internal/db` | `github.com/jackc/pgx` |
| `auth`, `connectors`, `normalize`, `resolve`, `ingest`, `jobs`, `documents`, `imports` | `internal/api` |
| `connectors` | `internal/resolve` |
| `resolve` | `internal/connectors`, `internal/ingest` |
| `normalize` | `internal/connectors` (connectors import normalize, never the reverse) |

## Troubleshooting

- `docker compose` not found with Homebrew's docker: add `"cliPluginsExtraDirs": ["/opt/homebrew/lib/docker/cli-plugins"]` to `~/.docker/config.json`.
- Port 5432 already in use: set `VITAMUX_DEV_PG_PORT` in `.env` and adjust `VITAMUX_DATABASE_URL`.
