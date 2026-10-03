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
