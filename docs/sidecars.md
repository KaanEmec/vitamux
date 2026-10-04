# Wrapping a third-party collector

How to plug an existing open-source collector (often in another language) into Vitamux without rewriting it. Contract: [connectors.md](architecture/connectors.md#remote-sidecar-mode) and [third-party collectors](architecture/connectors.md#third-party-collectors). Worked example: [`examples/sidecar-python/`](../examples/sidecar-python/) (stdlib only; read its README for the synthetic accepted values). Running one: [install.md](install.md#sidecars).

## Pick a path

| Question | Sidecar (remote connector) | Push collector |
| --- | --- | --- |
| What is the upstream? | A library or client that fetches on demand | A tool with its own scheduler and storage (it already runs, syncs and keeps state) |
| Who schedules, retries, rate-limits? | Vitamux (jobs, cursors, backfill, health) | The tool; the uploader only forwards |
| Who holds provider credentials? | Vitamux, sealed with the master key; the sidecar receives them per request | The tool; Vitamux never sees them |
| Sign-in flow | In the Vitamux wizard (password, MFA code, redirect) | In the tool |
| You write | A small HTTP server speaking `vitamux-connector/1`, plus a Go normalizer in the core | A small uploader plus a Go normalizer in the core |
| Pick it when | You want Vitamux to own sync, backfill and reauth | The tool is a service you would not rebuild, or Vitamux cannot hold its login |

Both paths keep raw responses verbatim and put normalization in Go in the core, so data stays reprocessable.
Unofficial upstreams use `Official=false`: the connection starts paused, the owner enables it, and a changed upstream shape is reported as `schema_drift`, never papered over.

## Wrap a library as a sidecar

Use the example as the skeleton; each step names the part to copy.

1. **Copy the template.** `cp -R sidecars/_template sidecars/<name>`, then add the manifest and lockfile (`pyproject.toml` + `uv.lock`, or `package.json` + `package-lock.json`) naming the upstream package. Commit the lockfile: it pins the exact version and hashes. Fill [`UPSTREAM.md`](#license-gate).
2. **Serve the protocol.** `GET /v1/describe`, `POST /v1/auth/begin|continue|refresh`, `POST /v1/fetch`, every request with `Authorization: Bearer <secret>`, every response with `Vitamux-Protocol: vitamux-connector/1`. Read the secret from the file named by `VITAMUX_SIDECAR_SECRET_FILE` (the template Compose file mounts it). Listen on `0.0.0.0:8080`.
   - `describe`: provider code (must equal the name given in `VITAMUX_SIDECARS`), display name, `official`, `auth_kind`, streams with cadence and limits, and `upstream: {package, version, source_url}` (the locked version; the core shows it and audits changes).
   - Auth: return a redirect or a `prompt` (fields of kind `text`, `password`, `code`), as the example does for username and password, then a code. Keep continuation state in the opaque `session` you return, not in memory: the core seals it and sends it back. Return rotated tokens in `credentials`.
   - `fetch`: stream NDJSON, one `raw` line per upstream response with a stable `external_key` and the **verbatim** body, then exactly one `result` line (`next_cursor`, `done`, `retry_after_s`, `credentials` if rotated). The core plans windows; you only fetch. A stream without a `result` line discards the page.
3. **Map errors.** Problem+json with `code` `reauth_required`, `rate_limited` (with `retry_after_s`), `transient`, `permanent` or `schema_drift` (with `endpoint` and a `fingerprint` of the unexpected shape). Fail closed: when the upstream returns something you do not recognise, raise `schema_drift`; never substitute other data.
4. **Never log or return secrets.** Not credentials, tokens, MFA codes or response bodies. `connector-test` checks this.
5. **Record cassettes.** Put synthetic recorded upstream responses (fake values only, marked `synthetic: true`) in `sidecars/<name>/testdata/`, where the [fixture guard](architecture/project.md) scans them. With `REPLAY=1` the server answers from `/app/testdata`, which the check mounts, instead of calling the upstream. Unit-test the wrapper against them in `tests/` (the Dockerfile's `test` stage).
6. **Write the normalizer in Go** (`internal/connectors/<name>/`, goldens from the cassettes) as in [adapters.md](adapters.md) steps 6 and 7, and register the provider through the sidecar: the core calls `register_provider` when `describe` succeeds, so no migration seeds it.
7. **Run the checks locally.** `scripts/sidecar-check.sh <name>` (see [Checks](#checks)).
8. **Add the Dependabot entry** (see [updates](#automatic-upstream-updates)) and open a PR.

Then enable it as in [install.md](install.md#sidecars): `VITAMUX_SIDECARS=<name>=http://sidecar-<name>:8080`, the shared secret, the Compose profile.

## Wrap a self-scheduling tool as a push collector

1. **Keep the tool as it is**, in its own container with its own storage, and run it unchanged. Pin it like a sidecar (version and digest); record it in `sidecars/<name>/UPSTREAM.md` so it passes the [license gate](#license-gate).
2. **Create a push connection** for it (Connections → Add push source, or `POST /api/v1/connections` with the provider code) and an ingest token scoped `ingest:<connection_id>`.
3. **Write the uploader**: read the tool's raw output (its files, database rows or API responses), and `POST /api/ingest/v1/batches` with a stable `external_key` per record and an `Idempotency-Key` per batch ([contract](architecture/connectors.md#push-ingest-contract)). Upload the response bodies unchanged. Keep an upload checkpoint, send `POST /api/ingest/v1/heartbeat`, and retry safely: same key and same bytes is a no-op.
4. **Write the normalizer in Go** in the core, as for a sidecar. There is no `describe`, no auth wizard and no scheduling by Vitamux.
5. Ship the uploader as a second container next to the tool (a Compose profile like the template's, without the shared secret; it holds the ingest token as a file secret).

## Replay a collector's archive

To move history that an old collector saved (raw responses on disk) into a live connection, for example before switching to a sidecar for the same account:

1. Connect the account through the sidecar first and leave the connection paused. Records dedupe per provider account (`account_key`), so the archive must land in that connection, not a separate push connection.
2. Convert the archive into ingest batch files ([schema](../schemas/ingest-batch.v1.json)): one `*.json` batch per file (at most 1,000 items each), with the connection's `conn_…` id, and each item shaped exactly as the sidecar's raw line for that record (same `stream`, `external_key`, `request` and body). Put binary items in `blobs/<sha256>` and reference them with `blob_sha256`. Set `provenance.migration_source` (e.g. `my_collector_archive`).
3. Run `vitamux import batches --dry-run DIR`. It validates every file and runs the normalizers in memory, reporting records, warnings and failures per stream. Fix the converter until nothing fails.
4. Run `vitamux import batches DIR`. Items already stored are skipped, so you can re-run it to import later additions. Then resume the connection: overlapping days from the live sync become no-ops or new raw versions, never copies.

## Layout

```text
sidecars/<name>/
  Dockerfile           # stages: build, test, final; base images pinned by digest
  UPSTREAM.md          # repo, package, version, tag, license, official or unofficial, license review
  compose.yaml         # profile sidecar-<name>: 256 MiB, read-only, healthcheck, frontend network only
                       # (bundled garmin and whoop live in deploy/compose and deploy/coolify instead)
  pyproject.toml, uv.lock   # or package.json, package-lock.json
  src/  tests/  testdata/   # testdata: synthetic cassettes
```

[`sidecars/_template/`](../sidecars/_template/) has the files to copy. The sidecar never gets database access: it joins only the `frontend` network, and vitamux reaches it there with the shared secret.

## License gate

The sidecar image is a separate work from the MIT core, so the core stays MIT whatever the upstream's license. `go run ./tools/notices -check` (CI step, and `-sidecars-only` for just this gate) reads every `sidecars/<name>/UPSTREAM.md` (the `_template` is skipped) and fails when:

- `UPSTREAM.md` is missing, or `Repository`, `Package`, `Version`, `Release tag`, `License` or `Status` is empty or still a `<placeholder>`;
- `Status` is not `official` or `unofficial`;
- `License` is an SPDX expression not covered by [`allowed-licenses.txt`](../tools/notices/allowed-licenses.txt) (copyleft, unknown, proprietary) and there is no line `- License review: reviewed YYYY-MM-DD by <who>: <outcome>`.

A review records why shipping the upstream in its own container is acceptable (no linking into the core, source offer for the image if the license requires it) and is a licensing decision made in the PR. The image carries `UPSTREAM.md` under `/usr/share/doc/vitamux-sidecar/`, so notices travel with it.

## Checks

`scripts/sidecar-check.sh <name> [git source]` runs, and CI runs it on every PR touching `sidecars/` and before every image publish:

1. the license gate;
2. the unit tests on cassettes (`docker build --target test`);
3. the image with `REPLAY=1` against `vitamux connector-test --url --secret-file [--scenario sidecars/<name>/conformance.json]` (schemas, auth steps, paging, cursors, replay, typed errors, no secrets in output). A [scenario](../schemas/connector-test-scenario.v1.json) holds synthetic sign-in values and which login provokes which error ([example](../examples/sidecar-python/conformance.json)); without one the kit checks the first auth step and fetches with placeholder credentials, which a replaying sidecar must serve;
4. the Go normalizer goldens in `internal/connectors/<name>/`.

## Automatic upstream updates

The lockfile pins the upstream release; Dependabot, the canary and the image workflow keep it current without a manual bump. All of them do nothing while `sidecars/` holds only `_template`.

- **Dependabot entry per sidecar.** Entries are per directory. Uncomment the template at the end of [`.github/dependabot.yml`](../.github/dependabot.yml) for `sidecars/<name>`: ecosystem `uv` or `npm`, `daily`, `cooldown` of 1 day (raise `default-days` for a risky upstream), and one group naming `Package` and its lockstep packages. Keep `Lockstep packages` in `UPSTREAM.md` in step with that group.
- **Auto-merge** ([`sidecar-automerge.yml`](../.github/workflows/sidecar-automerge.yml)). A Dependabot PR is approved and merged when `tools/sidecarscope` finds that it changed only the upstream package lines of one sidecar's manifest and lockfile, and every check is green. Any other dependency change, file change or red check leaves it open with the label `upstream-break`; the last good image stays `stable`. It needs the repository setting that lets Actions approve pull requests, and `ci` and `sidecar-ci` as required checks. It never checks out PR code.
- **Canary** ([`sidecar-canary.yml`](../.github/workflows/sidecar-canary.yml)). Nightly, each sidecar is built against the upstream's default branch (`Canary source` in `UPSTREAM.md`, default `git+<Repository>`; the Dockerfile's `UPSTREAM_GIT` build argument) and checked. A failure opens or updates one issue per sidecar; a pass closes it.
- **Images** ([`sidecar-image.yml`](../.github/workflows/sidecar-image.yml)). A merge changing `sidecars/<name>/` runs the checks, then publishes `ghcr.io/kaanemec/vitamux-sidecar-<name>` for amd64 and arm64 with an SBOM, a Trivy gate and, unless the repository variable `VITAMUX_SIGN_RELEASES` is `false`, a keyless signature. Tags: `<upstream-version>-<short-sha>` (immutable) and `stable` (moving, only green builds).

## Updates

The Compose profile follows `:stable`: `docker compose pull && docker compose up -d` picks up the newest upstream. To pin instead, set `VITAMUX_SIDECAR_<NAME>_IMAGE=ghcr.io/kaanemec/vitamux-sidecar-<name>@sha256:<digest>` in `.env`. Verify a signature as for the core image ([security.md](security.md#supply-chain)), with the identity `.github/workflows/sidecar-image.yml@refs/heads/main`.

## Do not

- Put credentials, tokens or real health data in cassettes, fixtures or logs. Fixtures are synthetic only.
- Interpret or diagnose in a normalizer. Sidecars move data; they never advise.
- Give a sidecar the database, the master key or the Docker socket.
