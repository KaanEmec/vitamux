# FAQ

## What is Vitamux?

A self-hosted, single-owner aggregator for your own health data. It collects from provider APIs, device bridges and file imports, keeps every original response, normalizes it into one provider-independent model in PostgreSQL, and lets you decide per metric which source wins through versioned rules that explain every value. It also turns blood-test PDFs into reviewed, structured lab results. One Go binary with an embedded UI, plus PostgreSQL ([overview](architecture/overview.md)).

## What is it not?

Not a medical device, coach or analytics suite. Vitamux never diagnoses, interprets or advises: it shows values, sources, units and the reference ranges the lab printed. Blood-test extraction is data entry with mandatory human review, not an assessment. It is also not a multi-user service: no sign-up, organizations or billing.

## Who owns the data?

You do. Everything stays in your PostgreSQL and data volume on hardware you run. Nothing leaves the host except calls to the providers you connect and, only if you configure and enable one and confirm per document, a lab PDF sent to the AI extraction provider you chose ([privacy controls](architecture/lab-documents.md#privacy-controls)). You can export all of it as NDJSON at any time ([exports](architecture/api.md#exports)) and delete a connection, a document or the whole account ([deletion](architecture/security.md#export-and-deletion)).

## Which sources work today?

- **Withings** (official API): blood pressure, weight and body composition, temperature, SpO2 and the other measures ([providers/withings.md](providers/withings.md)). Activity and sleep streams are not built yet.
- **Manual entries** through the API (`POST /api/v1/measurements/manual`).
- **Push uploads** through the ingest API, for clients and importers ([push ingest](architecture/connectors.md#push-ingest-contract)), and Vitamux's own export format (`vitamux import ndjson`).
- **Blood-test PDFs**, extracted by Gemini, OpenAI or a self-hosted OpenAI-compatible server and reviewed by you before anything is confirmed ([lab documents](architecture/lab-documents.md)).

New providers fit the same connector contract: see [adapters.md](adapters.md).

## What about Apple Health?

A backend cannot read HealthKit, so it needs an iPhone app: a Swift package plus a minimal SwiftUI app that uploads anchored, incremental batches. It is planned as the next epic, [E15](plan/E15-apple-health/README.md), and not part of v0.1 ([design](architecture/apple-health.md)).

## And Garmin, WHOOP, Oura?

Not in v0.1. Garmin and WHOOP have no official API usable by self-hosters; they are planned for v0.3.0 ([E18](plan/E18-garmin/README.md), [E19](plan/E19-whoop/README.md)) as optional, replaceable adapters in their own containers ([sidecar mode](architecture/connectors.md#remote-sidecar-mode)), marked unofficial, and failing closed when the provider changes its API. Oura, Ultrahuman and others are on the [deferred list](architecture/project.md#deferred-features).

## Why does a value come from one device and not another?

Because of the resolution rule for that metric. Each resolved value lists its inputs, the rule version and a plain-language explanation, and the all-sources view shows every source side by side. You can change the rule or set a manual override; both are versioned and reversible ([resolution](architecture/resolution.md)).

## What do I need to run it?

Docker Compose on a small Linux host with about 1 GiB of RAM for the stack, a domain name and a TLS reverse proxy ([install](install.md)). Back up the master key separately from the data: without it, provider tokens, documents and backups cannot be decrypted.

## Is it secure?

It is built for one internet-reachable owner: argon2id passwords, optional TOTP, scoped API keys, encrypted provider tokens and documents, a deny-by-default route matrix checked in CI, and logs without secrets or health values. Raw and normalized health data rely on your disk encryption. Details and residual risks: [security.md](security.md); reporting a vulnerability: [SECURITY.md](../SECURITY.md).

## What license?

MIT. Contributions are signed off under the DCO.
