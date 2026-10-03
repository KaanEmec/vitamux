# Vitamux

Vitamux is a self-hosted, open-source aggregator for your own health data. It collects measurements from provider APIs and uploads, keeps every original response, normalizes them into one provider-independent model in PostgreSQL, and lets you decide per metric which source wins, with an explanation for every value. One Go binary with an embedded web UI, plus PostgreSQL.

Vitamux never diagnoses, interprets or advises. It is for one owner, on hardware the owner controls.

## Features

- **Raw first**: every provider response is stored verbatim (content-addressed, compressed) before it is transformed, so data can be re-normalized when a normalizer improves.
- **Provenance**: each normalized value knows its provider, connection, device, origin, units, local date, dedupe key, raw payload and normalizer version. Corrections supersede; nothing is overwritten.
- **Resolution rules**: per metric and window (hour, local day, night, sleep episode, latest), typed and versioned rules such as `single_source`, `first_available`, `mean`, `max` or `latest`, with selectors by provider, origin, device type or device. Each result lists its inputs, coverage, rule version and a plain-language explanation; manual overrides are audited and reversible.
- **Withings** (official OAuth): blood pressure, weight, body composition and the other measures; hourly incremental sync, daily correction window, resumable backfills, optional notifications.
- **Push ingest API** for clients and importers, idempotent and schema-versioned; NDJSON export and import of all owner data.
- **Blood-test PDFs**: encrypted storage, extraction by Gemini, OpenAI or a self-hosted OpenAI-compatible model only after explicit per-document consent, and mandatory human review before anything is confirmed.
- **Configurator UI**: connections and their health, data and coverage, rule builder with previews, lab review, settings.
- **Operations**: PostgreSQL-only job queue and scheduler, health and readiness endpoints, private Prometheus metrics, daily backups with verified restore, master key rotation, hardened Docker Compose stack.

## Quick start

Docker Compose on a Linux host with a domain name and a TLS reverse proxy:

```sh
git clone https://github.com/KaanEmec/vitamux.git && cd vitamux/deploy/compose
cp .env.example .env && $EDITOR .env            # VITAMUX_PUBLIC_URL, VITAMUX_IMAGE
./init-secrets.sh
docker compose --profile setup run --rm init-secrets
docker compose up -d --wait
docker compose run --rm vitamux admin create-owner
```

The full guide, with reverse proxy examples (Caddy, Traefik, Nginx), Withings registration, backups and Coolify, is [docs/install.md](docs/install.md). Settings: [docs/configuration.md](docs/configuration.md). Questions: [docs/faq.md](docs/faq.md).

## Status

v0.1 is the first release: the MVP for a single owner. What is not there yet is listed in [known limitations](docs/release-notes/KNOWN_LIMITATIONS.md) (Apple Health arrives with [E15](docs/plan/E15-apple-health/README.md); Garmin and WHOOP later). Also worth knowing before you install:

- Only OAuth providers and push clients can be connected; a connector without OAuth has no connect flow yet ([adapters](docs/adapters.md#9-wire-it-into-the-binary)).
- Health data at rest relies on disk encryption; Vitamux encrypts provider tokens, TOTP secrets and documents, not raw or normalized rows, and does not encrypt backups.
- Login throttling is in memory, and the server runs as a single `serve` process.
- The Coolify variant follows Coolify's documented conventions but has not been deployed on a live Coolify instance yet.

Changes per release: [CHANGELOG.md](CHANGELOG.md).

## Documentation

- [docs/README.md](docs/README.md): map of all documentation.
- [docs/architecture/overview.md](docs/architecture/overview.md): design and decisions; [docs/security.md](docs/security.md): security model.
- [docs/api-reference.md](docs/api-reference.md): API operations; the contract is [api/openapi.yaml](api/openapi.yaml).
- [docs/operations/](docs/operations/): upgrade, backup, key rotation, troubleshooting.
- [docs/adapters.md](docs/adapters.md): writing a connector.

## Contributing

Setup, commands and conventions: [docs/development.md](docs/development.md) and [CONTRIBUTING.md](CONTRIBUTING.md). Fixtures are synthetic only; never commit real health data or credentials. Report vulnerabilities as described in [SECURITY.md](SECURITY.md). Community rules: [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

[MIT](LICENSE). Third-party licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
