# Project: open source, quality, budget, risks

## License and notices

- **MIT** for the core, Swift package, and app (owner decision, 2026-10-03). Contributions use the DCO (`Signed-off-by`), not a CLA.
- `THIRD_PARTY_NOTICES.md` is generated (`go-licenses`, `license-checker`) and copied into each image's `/licenses`. CI fails on unreviewed copyleft additions.

## Testing levels

| Level | Scope |
| --- | --- |
| Unit | Units, timezone, dedupe keys, rule validation, explanation templates |
| Golden | Every normalizer and extraction post-processor: synthetic raw → canonical JSON, version-guarded |
| Property | Resolution: density invariance within buckets, cached = live |
| Integration | Real PostgreSQL (17 and 18): ingest → normalize → resolve; leases, reaper; migrations from empty and from the previous release |
| Contract | OpenAPI responses; ingest schemas; authorization matrix |
| E2E | Playwright on a seeded synthetic stack |
| iOS | XCTest with a fake HealthStore; physical-device checklist |
| Perf and resources | 1-year synthetic dataset (≥ 6 M HR samples): query p95, RSS |
| Restore drill | backup → restore → compare → `resolve verify` |

## Synthetic fixtures policy

- All fixtures come from `tools/fixturegen` (deterministic seed) and carry a `synthetic: true` header.
- A CI guard runs gitleaks plus `tools/fixtureguard` (`make fixture-guard`), which scans every `fixtures/` and `testdata/` directory. Marker per file type: `.json` top-level `"synthetic": true`; `.ndjson`/`.jsonl` first line is a JSON object with `"synthetic": true`; any other text file (YAML key, `# synthetic: true`, `<!-- synthetic: true -->`) carries `synthetic: true` within its first 5 lines; binary files (PDF, FIT, zip) need a `<file>.synthetic` sidecar containing `synthetic: true` and are not content-scanned. Emails must use reserved domains (`example.com`, `*.test`, …); phone numbers and token-like strings are rejected, and synthetic credential values must contain `synthetic`. A line may opt out with `fixtureguard:allow`. Findings print `path:line: rule` without the matched text.
- Never commit real values, even "anonymized".

## Versioning and releases

- SemVer for `vitamux`. Independent versions for: ingest schema (`vitamux.ingest.batch/1`), rule schema (`vitamux.rule/1`), each normalizer (integer), and migrations (sequence).
- Release artifacts: multi-arch images, SBOMs, checksums, a Compose bundle with `.env.example`, a Swift package tag, and a changelog with upgrade notes.
- Contributor workflow: `make dev | test | lint | fixtures | golden`, conventional commits, and a PR template with privacy and security checklists.

## Resource budget

Targets, verified in J04.4 and J14.3:

| Profile | Idle RAM | Peak RAM | CPU | Disk |
| --- | --- | --- | --- | --- |
| Core (`vitamux` ≤ 80 MiB + postgres ≤ 270 MiB, `shared_buffers=128MB`) | ≤ 350 MiB | ≤ 900 MiB | 1 vCPU enough; idle < 2 % of a core | Images ≈ 0.5 GB; data 1–3 GB/yr |
| Core + one optional sidecar | + ≤ 150 MiB | ≤ 1.2 GiB | 2 vCPU recommended for backfills | + backups at 2–3× data |

Compose limits: `vitamux` 512 MiB, `postgres` 1 GiB, sidecars 256 MiB.

Memory stays bounded through batching: normalize 5,000 rows per transaction, COPY in chunks of 10,000, streaming exports.

## Deferred features

- Remote sidecar mode and unofficial collectors (Garmin, WHOOP): see [migration.md](migration.md).
- Withings activity and sleep streams; workout GPS routes (raw files kept).
- Weighted means, expression language, partitioning or dense series storage, S3 blob backend, HA.
- Passkeys (TOTP in MVP), multi-user UI, outbound webhooks, FHIR export.
- Ultrahuman, Oura, Android Health Connect (same push pattern), watchOS app, HealthKit write-back, App Store distribution.
- Any interpretation of health or lab data: never.

## Risks

| Risk | Mitigation |
| --- | --- |
| Rule semantics confuse users | Previews, explanations, all-sources view, conservative `first_available` defaults |
| Rotating refresh token lost on crash | Single-flight refresh, immediate persist, quick reauth |
| HealthKit background delivery is sparse | Foreground and observer sync, honest UI, physical-device tests |
| Apple developer account requirements | Verify early (Q2) |
| AI extraction errors or vendor privacy | Mandatory review, deterministic re-parse, opt-in consent, local provider option |
| High-frequency HR volume | Hourly aggregates, benchmarks, partitioning trigger |
| Master key loss | Init and backup docs, readiness check, `key_id` in manifest |
| Scope creep or single maintainer | Strict MVP boundary, deferral list, modular monolith |

## Assumptions to verify

| Assumption | Verified in |
| --- | --- |
| Withings: rotating refresh tokens, short-lived access tokens, `getmeas` `lastupdate` and paging, meastype codes, `value×10^unit`, notify `appli` codes, HEAD-validated callbacks, rate limits | J08.1 |
| HealthKit: hidden read-denial, `earliestPermittedSampleDate`, background-delivery entitlement and per-type frequencies, sleep category values | J15.1 |
| Storage and performance estimates in [data-model.md](data-model.md#volume-and-partitioning) | J04.4 |

## Open questions

1. ~~**Q1**~~ Resolved 2026-10-03: name **Vitamux**, license **MIT**, private GitHub repo `vitamux` (public later).
2. **Q2**: Is a paid Apple Developer Program membership available?
3. **Q3**: Owner timezone history since 2024, to seed `timezone_periods`.
4. **Q4**: Preferred AI extraction provider. Is sending complete lab PDFs to an external vendor acceptable at all, or local-only?
