# ADR-0017 Sidecar protocol `vitamux-connector/1`

Status: Accepted · Date: 2026-10-04 · Deciders: owner

## Context
Some providers are only reachable through an existing collector in another language. Such a collector runs as a sidecar container behind the connector contract ([connectors.md › Remote sidecar mode](../architecture/connectors.md#remote-sidecar-mode), [ADR-0001](0001-go-core.md)). Sidecars are built and released apart from the core, so the contract between them must be stable and language-neutral.

## Decision
- Protocol `vitamux-connector/1` is HTTP + JSON: [`api/connector-sidecar.v1.yaml`](../../api/connector-sidecar.v1.yaml) (OpenAPI 3.1) with messages in [`schemas/connector-sidecar.v1.json`](../../schemas/connector-sidecar.v1.json) and [synthetic examples](../../schemas/examples/connector-sidecar/). It is frozen as of this ADR.
- It maps one-to-one onto the Go contract (`Connector.Fetch`, `Authenticator.Refresh`, `Interactive.Begin/Continue`, `Descriptor`, typed errors). A test in `internal/connectors/remote` fails when a Go field, auth kind or error class has no single wire representation.
- The core plans and schedules; the sidecar describes itself, runs auth steps and fetches one page per call. It is stateless, has no database access, and receives credentials only with the call that needs them.
- A fetch page is NDJSON: raw lines (a JSON body verbatim, or a binary file base64-encoded with `sha256`, at most 25 MiB), then exactly one `result` or `error` line. Anything else discards the page as a transient protocol violation.
- Auth steps are a redirect (OAuth2) or a prompt (MFA, code challenge, device pairing), with an opaque `session` the core seals between steps.
- Versioning works as for push ingest: within v1, changes are additive and optional only, such as new optional fields or new error codes, which an older core treats as transient. Receivers ignore unknown fields; the schemas reject them so conformance checks catch typos. Removing, renaming, retyping or tightening anything needs `vitamux-connector/2` and a period in which the core speaks both.

## Alternatives considered
- **gRPC**: typed streaming, but it needs code generation and HTTP/2 tooling in every sidecar language, and is harder to debug with curl.
- **Sidecar stores raw data or schedules itself**: duplicates the raw-first pipeline and splits scheduling in two. That shape is the push collector path instead.
- **Problem+json only for errors**: cannot keep quarantined records before a schema drift, so a page ends with an `error` line instead.

## Consequences
- Sidecars in any language can be checked against the schemas and the conformance kit (J17.3).
- Every new optional field needs an update to the schema, the Go wire types and the mapping test, all within v1.
- Page, line and message size limits are enforced by the core and documented in the spec.
