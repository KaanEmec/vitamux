# Docker Compose deployment

The reference install: [`deploy/compose/compose.yaml`](../../deploy/compose/compose.yaml) runs `vitamux` and `postgres`, plus a one-shot `migrate`. Topology and decisions: [overview#deployment](../architecture/overview.md#deployment), [security#network-exposure](../architecture/security.md#network-exposure).

## Install

```sh
cd deploy/compose
cp .env.example .env && $EDITOR .env                   # set VITAMUX_PUBLIC_URL (https) and, if used, VITAMUX_IMAGE
./init-secrets.sh                                      # random DB passwords and URLs into ./secrets
docker compose --profile setup run --rm init-secrets   # master key, once, into the vitamux-secrets volume
docker compose up -d --wait                            # postgres -> migrate -> vitamux
docker compose run --rm vitamux admin create-owner     # first owner (reads the TTY)
```

Point your reverse proxy at `127.0.0.1:8080` (change with `VITAMUX_PORT`). It must terminate TLS for `VITAMUX_PUBLIC_URL`, which is also where OAuth callbacks (`/oauth/<provider>/callback`) and webhooks (`/webhooks/...`) arrive. Set `VITAMUX_TRUSTED_PROXIES` to the proxy's address as the container sees it (for a host proxy on Linux, the `frontend` network gateway: `docker network inspect vitamux_frontend`), otherwise `X-Forwarded-*` is ignored.

Back up `secrets/` and the master key separately from database backups. Losing the master key loses provider tokens and encrypted documents ([security#keys](../architecture/security.md)). `init-secrets` refuses to overwrite an existing key.

## What is locked down

Enforced by the policy test `deploy/compose/compose_test.go` (runs in the `go` CI job):

- PostgreSQL has no published port and sits on an `internal` network (no route out); `migrate` too. Only `vitamux` is also on a normal network, for provider API calls, and publishes only `127.0.0.1:<port>`.
- Every container: `read_only` rootfs (tmpfs for `/tmp`, writable only `/data` and the pgdata volume), `cap_drop: [ALL]`, `no-new-privileges`, non-root user, `mem_limit` from [project#resource-budget](../architecture/project.md#resource-budget) (vitamux 512 MiB, postgres 1 GiB), a healthcheck, rotated JSON logs.
- Secrets are Compose secrets read through `*_FILE` settings; no credential is in the Compose file or the environment. `vitamux` gets only the DML-only `vitamux_app` URL; the DDL `vitamux_owner` URL is mounted into `migrate` alone. Roles are created on first start from [`deploy/sql/roles.sql`](../../deploy/sql/roles.sql); rotating a role password later is an `ALTER ROLE` plus editing the secret file.
- The healthcheck is `vitamux healthcheck` (GET `/readyz`; the distroless image has no curl). `stop_grace_period: 60s` lets `serve` drain jobs on `SIGTERM` ([reliability#upgrades](../architecture/reliability.md#upgrades)).
- The CI `image` job scans the built image with Trivy and fails only on CRITICAL findings that have a fix.

## Upgrade

```sh
docker compose pull && docker compose up -d --wait
```

`migrate` runs first and `vitamux` starts only if it succeeded. Read the changelog's upgrade notes before crossing a minor version.

## Files and permissions

Secret files are mode 0444 inside a 0700 `secrets/` directory: Compose cannot chown file secrets to the container user, so the directory is what keeps other host users out. The master key lives in a named volume instead because `vitamux` refuses a key file readable by group or others. If you replace `vitamux-data` with a bind mount, `chown 65532:65532` it.

## Coolify

Coolify deploys the same file as a Compose application: give the `vitamux` service its domain (container port 8080) and let Coolify's proxy do TLS; the `127.0.0.1` publish is harmless there. The `secrets/*` files must exist next to the file at deploy time. A Coolify-specific section is verified on a fresh instance in [J14.2](../plan/E14-release-v0.1/J14.2-docs-set.md) and [J14.4](../plan/E14-release-v0.1/J14.4-release-engineering.md).
