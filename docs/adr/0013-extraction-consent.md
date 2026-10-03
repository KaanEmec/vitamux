# ADR-0013 Extraction providers and consent

Status: Accepted · Date: 2026-10-03 · Deciders: owner

## Context
Blood-test extraction may send a private PDF to a third-party model. The product must not lock into one vendor, must work offline for CI and demos, and must never send a document without the owner knowing where it goes. Details: [lab-documents.md › Extraction provider interface](../architecture/lab-documents.md#extraction-provider-interface) and [› Privacy controls](../architecture/lab-documents.md#privacy-controls).

## Decision
- **One interface** (`internal/documents/extract.Extractor`: `ID`, `External`, `Model`, `Extract`) with four providers: `fake` (fixture ground truth by PDF sha256), `gemini`, `openai` (Responses API) and `openai_compatible` (same call, admin-set base URL). Every provider gets the same embedded prompt and schema; every answer goes through `documents.DecodeExtraction`. Provider-specific code is one file each.
- **Three gates for external providers**, all checked server-side when the run is requested and again when the job starts: (1) configured by the admin (`VITAMUX_<P>_API_KEY_FILE` + `VITAMUX_<P>_MODEL`, or base URL + model), (2) enabled by the owner (`documents.external_ai.<provider>.enabled`), (3) per-request `consent {provider, model, acknowledged_at}` naming exactly the configured provider and model. Missing or stale gate → 403 (1, 2) or 409 `consent_required` (3). `fake` needs none. `openai_compatible` counts as external.
- **What is kept**: the consent, actor, provider, model, prompt and schema versions on `extraction_runs`; the raw response sealed with the document key (shredded with the document), also for invalid answers. Never logged or stored in the clear: keys, PDFs, responses, provider error messages (only status and error code).

## Alternatives considered
- Consent as a boolean flag — does not prove the owner saw which provider and model receive the PDF; a model change after enablement would pass silently.
- Enablement only, no per-request consent — one setting would authorize every future upload.
- Vendor SDKs — larger dependency surface and logging we do not control; the plain HTTP calls are small.
- Gemini Files API — keeps a copy at Google for 48 hours; inline PDFs (≤ 14 MiB) avoid that.

## Consequences
- Changing a model name invalidates queued runs (`consent_mismatch`) and requires new consent.
- The UI (J12.6) must show provider and model before asking, and send them back verbatim.
- Real adapters are tested against recorded request and response shapes, not live APIs; a provider API change surfaces as `rejected` or `invalid_output`, never as silently different data.
