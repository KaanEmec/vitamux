# Blood-test documents

Extraction is structured data entry with mandatory human confirmation. The product **never interprets** results.

## Storage

- Accept PDF only: magic bytes `%PDF-` plus a pure-Go structure check (`documents.Validate`). Defaults: ≤ 20 MiB, ≤ 50 pages. Encrypted PDFs are rejected. No server-side rendering. A refused upload answers 413 `payload_too_large`, or 422 with the reason (`not_pdf`, `empty`, `encrypted`, `too_many_pages`, `malformed`) in `errors[0].detail`.
- The check is a scan, not a parser: `/Encrypt` anywhere rejects; pages are `/Type /Page` objects in the body and in FlateDecode object streams (inflated up to 64 MiB). A page repeated by an incremental update counts twice, and object streams with other filters are not read.
- Each document gets a random AES-256-GCM data key, wrapped by the master key (`document_keys`, purpose `documents`, AAD `document_keys:<id>`; `vitamux keys rotate` rewraps it). The PDF, the original filename, and anything else holding document content (raw extractor responses: `Store.Seal`) are sealed with the data key before they are stored, so the blob hash says nothing about the PDF.
- Deletion destroys the wrapped key (crypto-shred), tombstones the row (no hash, blob or filename), drops extraction runs and releases the blobs for the next sweep. `derived=keep` keeps confirmed lab results, which copy the versions and evidence they need; `derived=delete` removes them too (also later, on the tombstone). The audit event holds counts only. Old backups, and PostgreSQL pages not yet vacuumed, keep the wrapped key until they rotate out.
- Metadata: sha256 (a re-upload of the same content links to the live document), size, encrypted original filename, `uploaded_at`, `retention_until`, status `uploaded → extracting → needs_review → confirmed | deleted`.

## Extraction provider interface

Package `internal/documents/extract` ([ADR-0013](../adr/0013-extraction-consent.md)):

```go
type Extractor interface {
    ID() string        // "fake" | "gemini" | "openai" | "openai_compatible"
    External() bool    // true ⇒ document leaves the host (also openai_compatible)
    Model() string     // configured model, which consent must name
    Extract(ctx context.Context, req Request) (Response, error)
}
// Request: PDF, Pages, Prompt (prompts/lab-extraction/v1.md), Schema (schemas/lab-extraction.v1.json), both embedded
// Response: Raw (stored sealed, also when invalid), Extraction (rows, document fields, warnings), ModelID, RequestID, Usage
```

| Provider | Notes |
| --- | --- |
| `fake` | Deterministic: the committed ground truth for the synthetic fixture PDFs, by sha256. Used in CI and demos; any other PDF fails with `unknown_document`. |
| `gemini` | `generateContent` with the PDF inline (≤ 14 MiB; no Files API) and `responseJsonSchema`; key in the `x-goog-api-key` header. |
| `openai` | Responses API, PDF as `input_file`, strict `json_schema` output, `store: false`. |
| `openai_compatible` | The same call against `VITAMUX_OPENAI_COMPATIBLE_BASE_URL` (key optional), for self-hosted servers that implement the Responses API with file input. |

The schema sent to providers drops the fixture-only `synthetic` key and keywords structured-output APIs reject; `documents.DecodeExtraction` still enforces the full contract on every answer.

`POST /api/v1/documents/{id}/extractions` queues an `extract_document` job (one active per document; a second request answers 409 `conflict`); `GET` on the same path lists runs without raw responses. The job retries `transient` and `rate_limited` failures (Retry-After up to 15 min reschedules) up to 3 attempts. Other failures end the run with `error_class` (`auth`, `rejected`, `too_large`, `refused`, `invalid_output`, `unknown_document`, `provider_unavailable`, `provider_disabled`, `consent_mismatch`, `abandoned`), keep the document, and return it to `uploaded` (or `needs_review` if an earlier run succeeded). Success stores the rows in `lab_extracted_rows` and moves the document to `needs_review`.

## Extracted row schema (v1)

`page`, `row_index`, `analyte_label` (verbatim), `value_text`, `value_numeric`, `comparator` (`< > <= >=`), `unit_text`, `reference_range_text`, `ref_low`, `ref_high`, `printed_flag` (verbatim), `specimen_type`, `collected_at`, `reported_at`, `laboratory`, `evidence_text`, `bbox` (page fractions, or null), `confidence` (a hint only), `warnings[]`.

Contract: [`schemas/lab-extraction.v1.json`](../../schemas/lab-extraction.v1.json) (`vitamux.lab.extraction/1`; Go: `documents.DecodeExtraction`), with document-level `laboratory`, `specimen_type`, dates as ISO local time plus the printed text, `page_count` and `warnings[]`. Every key is present; null means not printed or unreadable (`unreadable_*` warning). Dates whose day and month order is ambiguous stay null with the text kept. Patient identifiers are never extracted. Prompt: [`prompts/lab-extraction/v1.md`](../../prompts/lab-extraction/v1.md), transcription only, reviewed per version ([REVIEW.md](../../prompts/lab-extraction/REVIEW.md)). Test corpus: [fixtures/README.md#lab-reports](../../fixtures/README.md#lab-reports).

## Validation

Deterministic checks after extraction:

- re-parse value, comparator, and range from verbatim text and flag disagreements;
- check that `evidence_text` exists in the PDF text layer (otherwise `evidence_unverified`);
- `unknown_unit`, `date_in_future`, `date_ambiguous`, duplicate analyte rows;
- suggest an analyte via `analyte_aliases`, never final until confirmed.

## Review and confirmation

- The UI shows the PDF page (pdf.js) with evidence highlighted beside an editable row table. Every row must be accepted, edited, or rejected. Required fields: label, value, unit (or explicit unitless), collection date. Each edit is recorded in `extraction_row_edits`.
- `confirm` atomically creates `lab_reports` and `lab_results`. Each result keeps:
  - original label, value text, unit, and printed range and flag;
  - numeric value; canonical analyte and value **only** if an analyte-specific conversion exists;
  - page, evidence, run, and prompt/model/normalizer versions.
- Later edits create `lab_result_revisions`. Re-extraction creates a new run, and runs can be compared.
- The UI shows printed ranges and flags only. No scores, judgements, or advice.

## Privacy controls

- External providers are off by default. The admin configures one with `VITAMUX_GEMINI_API_KEY_FILE` + `VITAMUX_GEMINI_MODEL`, `VITAMUX_OPENAI_API_KEY_FILE` + `VITAMUX_OPENAI_MODEL`, or `VITAMUX_OPENAI_COMPATIBLE_BASE_URL` + `VITAMUX_OPENAI_COMPATIBLE_MODEL` (optional `..._API_KEY_FILE`; https on a public host unless `VITAMUX_OPENAI_COMPATIBLE_ALLOW_PRIVATE=true`). The owner enables it with the setting `documents.external_ai.<provider>.enabled=true` (`extract.SetEnabled`, audited). Each extraction also requires `consent {provider, model, acknowledged_at}` naming the configured provider and model. Not configured or disabled → `403 forbidden`; missing or mismatched consent → `409 consent_required`. Both are checked again when the job starts.
- Stored on `extraction_runs`: consent, actor, prompt and schema versions, model id, provider request id, token usage, timestamps; the `document.extract` audit event names provider, model and consent time. Never logged: keys, document bytes, responses, provider error messages (only HTTP status and error code).
- PDF text is untrusted input (prompt injection). The model has no tools, output must match the schema, and a human reviews everything.
- Retention: owner settings `documents.retention_days` (default keep) and `documents.delete_original_after_confirmation`, set through `documents.SetPolicy`, which applies the period to every live document's `retention_until`. The daily `document_retention` job deletes due originals with `derived=keep`. Deletion modes: `derived=keep|delete`.
