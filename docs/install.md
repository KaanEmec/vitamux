# Install

Run Vitamux with Docker Compose on any Linux host you control: one `vitamux` container, PostgreSQL, and your own TLS reverse proxy in front. What the Compose file locks down is in [deploy/compose.md](deploy/compose.md); every setting is in [configuration.md](configuration.md). For Coolify, read [Coolify](#coolify) instead of steps 3 to 5.

## Requirements

- Docker Engine with the Compose v2 plugin (`docker compose version`).
- A DNS name for Vitamux pointing at the host, and a reverse proxy that terminates TLS for it (examples below). Providers such as Withings require https and a domain name for callbacks.
- About 1 GiB of RAM and 1 vCPU for the stack, a few GB of disk per year of data plus 2–3× that for backups ([resource budget](architecture/project.md#resource-budget)).
- Open only ports 80 and 443 to the internet. PostgreSQL is never published and `vitamux` listens on `127.0.0.1`.

## 1. Get the Compose files

Clone the repository (or unpack a release's Compose bundle) and work in `deploy/compose/`. Keep the layout: `compose.yaml` mounts `../sql/roles.sql`.

```sh
git clone https://github.com/KaanEmec/vitamux.git && cd vitamux/deploy/compose
```

## 2. Configure

```sh
cp .env.example .env
```

Edit `.env`:

- `VITAMUX_PUBLIC_URL`: the https URL users will open, e.g. `https://vitamux.example.com` (no trailing path). OAuth callbacks and webhook URLs are built from it, so change it only together with your provider registrations.
- `VITAMUX_IMAGE`: pin a release tag, e.g. `ghcr.io/kaanemec/vitamux:v0.1.0` (tags are `v`-prefixed; `latest` follows final releases).
- `VITAMUX_WITHINGS_CLIENT_ID`, if you connect Withings ([step 6](#6-connect-withings)).

The other settings can stay at their defaults.

## 3. Start the stack

```sh
./init-secrets.sh                                      # random DB passwords and URLs into ./secrets (never overwrites)
docker compose --profile setup run --rm init-secrets   # master key, once, into the vitamux-secrets volume
docker compose up -d --wait                            # postgres -> migrate -> vitamux
```

`init-secrets` prints the master key's id and refuses to overwrite an existing key. **Back the key up now** (step 7): without it, provider tokens and documents cannot be decrypted and backups cannot be restored.

If you use Withings, paste your application's client secret into `secrets/withings_client_secret` before `up` (or run `docker compose up -d --wait` again afterwards).

## 4. Create the owner

Vitamux has one owner account and no sign-up page:

```sh
docker compose run --rm vitamux admin create-owner
```

It asks for a username and a password (at least 12 characters) on the terminal; they are never taken from flags or the environment. Sign in at your public URL once the proxy is up, and enable two-factor authentication under Settings → Security. `admin reset-password` works the same way and ends every session.

## 5. Reverse proxy

Point the proxy at `127.0.0.1:8080` (change the host port with `VITAMUX_PORT`) for the host in `VITAMUX_PUBLIC_URL`. It must pass every path through: the UI and `/api` for you, `/oauth/<provider>/callback` and `/webhooks/...` for providers, and `/healthz`, `/readyz` for monitoring. Uploads are up to 25 MiB.

Set `VITAMUX_TRUSTED_PROXIES` in `.env` to the proxy's address as the container sees it, so Vitamux believes its `X-Forwarded-For`/`-Proto` headers (used for login throttling per address). For a proxy on the host, that is the gateway of the `frontend` network: `docker network inspect vitamux_frontend --format '{{(index .IPAM.Config 0).Gateway}}'`. Then `docker compose up -d --wait`.

Webhook URLs carry a secret token. Keep them out of proxy access logs where your proxy allows it (the Nginx example does).

**Caddy** (gets and renews certificates by itself):

```caddyfile
vitamux.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Caddy writes no access log unless you add a `log` directive.

**Traefik** (file provider, with an existing `websecure` entry point and a certificate resolver named `le`):

```yaml
http:
  routers:
    vitamux:
      rule: Host(`vitamux.example.com`)
      entryPoints: [websecure]
      tls: {certResolver: le}
      service: vitamux
  services:
    vitamux:
      loadBalancer:
        servers:
          - url: http://127.0.0.1:8080
```

If Traefik runs in Docker, `127.0.0.1` is Traefik's own container: attach `vitamux` to Traefik's network in a `compose.override.yaml` and use `http://vitamux:8080` instead, with `VITAMUX_TRUSTED_PROXIES` set to that network's subnet.

**Nginx** (certificates managed separately, e.g. with certbot):

```nginx
server {
    listen 443 ssl;
    http2 on;
    server_name vitamux.example.com;
    ssl_certificate     /etc/letsencrypt/live/vitamux.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/vitamux.example.com/privkey.pem;
    client_max_body_size 26m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 120s;
    }
    location /webhooks/ {
        access_log off; # the path holds a secret token
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Check: `curl -fsS https://vitamux.example.com/readyz` answers `{"status":"ok",...}`. If not, see [troubleshooting](operations/troubleshooting.md#readyz-answers-503).

## 6. Connect Withings

Each install uses its own Withings developer application ([details](providers/withings.md#app-registration-and-callback)):

1. Create an application in the Withings developer dashboard and register this **callback URL**: `https://vitamux.example.com/oauth/withings/callback` (your `VITAMUX_PUBLIC_URL` + `/oauth/withings/callback`).
2. Put the client id in `.env` (`VITAMUX_WITHINGS_CLIENT_ID`) and the client secret in `secrets/withings_client_secret`, then `docker compose up -d --wait`.
3. In the UI, open Connections → **Connect a source** → Withings. The first sync fetches the whole history.
4. Optional, for lower latency: turn on Withings notifications under Settings. Vitamux then subscribes **webhook URLs** of the form `https://vitamux.example.com/webhooks/withings/<token>` through the Withings API itself; nothing more needs registering in the dashboard, but your proxy must pass `/webhooks/` through. Hourly polling continues either way.

## 7. Backups

The stack writes a daily backup to the `vitamux-backups` volume and keeps three ([backup](operations/backup.md), including off-host encryption and restore). Two things are **not** in those backups and must be stored elsewhere, e.g. a password manager:

- `deploy/compose/secrets/` (database passwords and the Withings secret);
- the master key: `docker run --rm -v vitamux_vitamux-secrets:/s:ro busybox cat /s/master.key > master.key && chmod 600 master.key`, then move that file off the host.

## Sidecars

Optional third-party sources (a collector wrapped as a sidecar, [guide](sidecars.md)) run as containers next to `vitamux`. They get no database access, 256 MiB, a read-only root and only the `frontend` network. Their image follows `:stable`, so a pull brings the newest upstream release that passed its checks. Unofficial sources start paused: enable the connection in the UI after reading its warning.

**Bundled: Garmin and WHOOP.** Both Compose files ship them, off. Vitamux always lists them; until one runs it shows `needs_sidecar` with the line below for your install, and finds the sidecar on its own once it runs (**Check again**, or within a minute). Their shared secrets are generated for you (a one-shot on every start, into a volume per sidecar that only `vitamux` and that sidecar mount); nothing goes in `.env` but the switch:

| Install | Turn on | Then |
| --- | --- | --- |
| Compose | `COMPOSE_PROFILES=garmin,whoop` in `deploy/compose/.env` (or just one) | `docker compose up -d --wait` |
| Coolify | Environment variables `GARMIN_SIDECAR=1` and/or `WHOOP_SIDECAR=1` | Redeploy |

Coolify gets a replica count instead of a profile because it does not reliably honour Compose profiles ([coollabsio/coolify#6395](https://github.com/coollabsio/coolify/issues/6395)); the toggle was checked with Docker Compose, not yet on a live Coolify instance. To pin an image, set `VITAMUX_SIDECAR_GARMIN_IMAGE` or `VITAMUX_SIDECAR_WHOOP_IMAGE` to `...@sha256:<digest>`; to update on a schedule, run `docker compose pull && docker compose up -d` from cron or a systemd timer (Coolify: redeploy).

**Your own sidecar.** Add it in the panel with a name and a private-network URL; Vitamux generates the shared secret and shows it once, to give the sidecar as its `VITAMUX_SIDECAR_SECRET_FILE` ([`POST /api/v1/sidecars`](api-reference.md)). Run its container next to `vitamux` from [`sidecars/_template/compose.yaml`](../sidecars/_template/compose.yaml). Automated installs can register it with `VITAMUX_SIDECARS` and a secret file instead ([configuration](configuration.md#providers)); such sidecars show read-only in the panel.

The first connect runs the sidecar's own sign-in (password, MFA code or redirect).

Failures show per connection (degraded or needs re-auth), never for the whole stack; see [troubleshooting](operations/troubleshooting.md). Resource budget: [resource-budget.md](resource-budget.md).

## Upgrades and operations

- New releases: [operations/upgrade.md](operations/upgrade.md).
- Rotating the master key: [operations/key-rotation.md](operations/key-rotation.md).
- When something is red: [operations/troubleshooting.md](operations/troubleshooting.md).

## Coolify

Coolify deploys [`deploy/coolify/compose.yaml`](../deploy/coolify/compose.yaml), a variant of the release file that follows Coolify's Compose conventions (checked against its documentation on 2026-10-04; it shares the policy test of `deploy/compose/`). The differences:

| Release file | Coolify file |
| --- | --- |
| Publishes `127.0.0.1:8080` for your proxy | No published ports; Coolify's proxy routes the service's domain to port 8080 and handles TLS |
| `VITAMUX_PUBLIC_URL` from `.env` | From Coolify's `SERVICE_URL_VITAMUX`, i.e. the domain you give the `vitamux` service |
| Secret files in `./secrets` from `init-secrets.sh`, master key from a manual `init-secrets` run | One-shot services generate the database passwords and the bundled sidecars' secrets (`secrets`) and the master key (`master-key`, `init-secrets --if-missing`) into named volumes on every deploy, never overwriting; each volume is mounted only where the release file mounts that secret |
| Withings secret in `secrets/withings_client_secret` | Coolify environment variable `WITHINGS_CLIENT_SECRET`, seen only by the offline `secrets` service, which writes it to a file for `vitamux` |
| Init SQL mounted from `deploy/sql/` | Inlined with Coolify's `content:` (a test keeps it in step) |

Steps:

1. Create a resource of type Docker Compose from this public Git repository with the Compose file `/deploy/coolify/compose.yaml`, or paste the file into an empty Docker Compose resource.
2. Under Domains, edit the `vitamux` service's domain: protocol `https`, your domain (e.g. `vitamux.example.com`), port `8080`; Noindex is a good idea. Coolify serves it on 443 and fills `SERVICE_URL_VITAMUX` (that is `VITAMUX_PUBLIC_URL`) from it on the next deploy. `SERVICE_URL_VITAMUX_8080` may keep a generated placeholder; Vitamux does not read it. After deploying, `/healthz` over HTTPS carries a `Strict-Transport-Security` header only when the public URL is right.
3. Set environment variables: `VITAMUX_IMAGE` (a pinned tag), and for Withings `VITAMUX_WITHINGS_CLIENT_ID` and `WITHINGS_CLIENT_SECRET` (mark it as a secret). Optional: `VITAMUX_TRUSTED_PROXIES` with the subnet of Coolify's proxy network (`docker network inspect coolify`).
4. Deploy. `secrets`, `master-key` and `migrate` run and exit; `vitamux` turns healthy. Volumes persist across redeploys, so the generated secrets and the key stay.
5. Create the owner from the server's shell (the image has no shell, so Coolify's web terminal cannot attach): `docker exec -it <vitamux container> /vitamux admin create-owner`.
6. Back up the master key from the server's shell as in [step 7](#7-backups), with the volume name Coolify gave `vitamux-secrets` (`docker volume ls`). The database secrets live in the `pg-secrets`, `migrate-secret` and `app-secret` volumes; back them up the same way or reset role passwords with `ALTER ROLE` after a restore.
7. Register the Withings callback URL as in [step 6](#6-connect-withings).

Daily backups go to the `vitamux-backups` volume as above; Coolify's own backup feature covers its database resources, not PostgreSQL inside a Compose resource. A restore runs from the server's shell with the `restore` profile service of the Coolify file ([backup#restore](operations/backup.md#restore)). Upgrades: change `VITAMUX_IMAGE` and redeploy ([upgrade](operations/upgrade.md#coolify)).

Networking, as seen on a live Coolify instance: Coolify adds every service to the resource's own network, which its proxy joins too. So PostgreSQL is reachable from the resource's services and from the proxy, but not from other Coolify resources, and it publishes no port.
