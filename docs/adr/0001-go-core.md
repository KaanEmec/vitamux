# ADR-0001 Go for the core; other languages only in optional sidecars

Status: Accepted · Date: 2026-10-03 · Deciders: owner (G0 approval)

## Context
The core is I/O-bound: HTTP, SQL, scheduling, normalization, and resolution for one owner. It must be small to deploy, easy to build for amd64/arm64, and approachable for a small contributor community. Some provider clients exist only in other languages (e.g., TypeScript). See [overview.md › Key decisions](../architecture/overview.md#key-decisions).

## Decision
Write the core in **Go** (current stable toolchain, pinned in `go.mod`): `pgx` v5 + `sqlc`, `goose`, stdlib `net/http` routing, `log/slog`, and a `CGO_ENABLED=0` static binary. Other languages are allowed only in optional, replaceable sidecars behind the connector contract ([connectors.md › Execution modes](../architecture/connectors.md#execution-modes)).

## Alternatives considered
- **Rust**: saves ~20–40 MiB RSS, which is small next to PostgreSQL's 150–300 MiB, and has stronger types. It costs slower development and builds, harder cross-compilation, and a smaller contributor pool.
- **TypeScript/Node core**: would reuse JS clients, but uses more memory, and its runtime and dependency churn is a poor fit for a long-lived self-hosted daemon.

## Consequences
- Weaker sum types are mitigated by JSON Schema validation, the `exhaustive` linter, and table tests.
- Contributors need Go for backend work, and Node only to build the web UI.
