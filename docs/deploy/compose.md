# Docker Compose deployment

The reference install: [`deploy/compose/compose.yaml`](../../deploy/compose/compose.yaml) runs `vitamux` and `postgres`, plus a one-shot `migrate`. Topology and decisions: [overview#deployment](../architecture/overview.md#deployment), [security#network-exposure](../architecture/security.md#network-exposure).

## Install

Step by step, including the reverse proxy, the first owner and Withings: [install.md](../install.md). The short version, in `deploy/compose/`: `cp .env.example .env` (set `VITAMUX_PUBLIC_URL`), `./init-secrets.sh`, `docker compose --profile setup run --rm init-secrets`, `docker compose up -d --wait`, `docker compose run --rm vitamux admin create-owner`.

Daily backups go to the `vitamux-backups` volume ([operations/backup.md](../operations/backup.md)). Back up `secrets/` and the master key separately from them. Losing the master key loses provider tokens and encrypted documents ([security#keys](../architecture/security.md)). `init-secrets` refuses to overwrite an existing key.

## What is locked down

Enforced by the policy test `deploy/compose/compose_test.go` (runs in the `go` CI job) for this file and the Coolify variant:

- PostgreSQL has no published port and sits on an `internal` network (no route out); `migrate` too. Only `vitamux` is also on a normal network, for provider API calls, and publishes only `127.0.0.1:<port>`.
- Every container: `read_only` rootfs (tmpfs for `/tmp`, writable only `/data`, `/backups` and the pgdata volume), `cap_drop: [ALL]`, `no-new-privileges`, non-root user, `mem_limit` from [project#resource-budget](../architecture/project.md#resource-budget) (vitamux 512 MiB, postgres 1 GiB), a healthcheck, rotated JSON logs.
- Secrets are Compose secrets read through `*_FILE` settings; no credential is in the Compose file or the environment. `vitamux` gets only the DML-only `vitamux_app` URL; the DDL `vitamux_owner` URL is mounted into `migrate` alone. Roles are created on first start from [`deploy/sql/roles.sql`](../../deploy/sql/roles.sql); rotating a role password later is an `ALTER ROLE` plus editing the secret file.
- The healthcheck is `vitamux healthcheck` (GET `/readyz`; the distroless image has no curl or shell, only `pg_dump`/`pg_restore` for backups). `stop_grace_period: 60s` lets `serve` drain jobs on `SIGTERM` ([reliability#upgrades](../architecture/reliability.md#upgrades)).
- The CI `image` job scans the built image with Trivy and fails only on CRITICAL findings that have a fix.

## Sidecars

The bundled Garmin and WHOOP sidecars are services of this file behind the profiles `garmin` and `whoop`; other sidecars are overlay files like [`sidecars/_template/compose.yaml`](../../sidecars/_template/compose.yaml) ([install#sidecars](../install.md#sidecars)). Policy for them, as in the template ([sidecars.md](../sidecars.md#layout)): no database network (`frontend` only), 256 MiB, read-only root, `cap_drop: ALL`, `no-new-privileges`, a healthcheck, and a shared secret file instead of an environment value.

## Upgrade

`docker compose pull && docker compose up -d --wait`: `migrate` runs first and `vitamux` starts only if it succeeded. Backup first, rollback rules and the drain: [operations/upgrade.md](../operations/upgrade.md).

## Files and permissions

Secret files are mode 0444 inside a 0700 `secrets/` directory: Compose cannot chown file secrets to the container user, so the directory is what keeps other host users out. The master key lives in a named volume instead because `vitamux` refuses a key file readable by group or others. If you replace `vitamux-data` with a bind mount, `chown 65532:65532` it.

## Coolify

[`deploy/coolify/compose.yaml`](../../deploy/coolify/compose.yaml) is the same stack for Coolify: no published ports, secrets generated into volumes by one-shot services, the public URL from Coolify's domain. The policy test covers it too (no published port at all; each secret volume mounted only where the release file mounts that secret). One exception to the list above: its `secrets` one-shot runs as root, with no capabilities, no network and a read-only rootfs, because it writes into freshly created root-owned volumes. Setup: [install#coolify](../install.md#coolify).
