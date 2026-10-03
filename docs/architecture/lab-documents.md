# Blood-test documents

Extraction is structured data entry with mandatory human confirmation. The product **never interprets** results.

## Storage

- Accept PDF only: magic bytes `%PDF-` plus a pure-Go structure check (`documents.Validate`). Defaults: ≤ 20 MiB, ≤ 50 pages. Encrypted PDFs are rejected. No server-side rendering. A refused upload answers 413 `payload_too_large`, or 422 with the reason (`not_pdf`, `empty`, `encrypted`, `too_many_pages`, `malformed`) in `errors[0].detail`.
- The check is a scan, not a parser: `/Encrypt` anywhere rejects; pages are `/Type /Page` objects in the body and in FlateDecode object streams (inflated up to 64 MiB). A page repeated by an incremental update counts twice, and object streams with other filters are not read.
- Each document gets a random AES-256-GCM data key, wrapped by the master key (`document_keys`, purpose `documents`, AAD `document_keys:<id>`; `vitamux keys rotate` rewraps it). The PDF, the original filename, and anything else holding document content (raw extractor responses: `Store.Seal`) are sealed with the data key before they are stored, so the blob hash says nothing about the PDF.
- Deletion destroys the wrapped key (crypto-shred), tombstones the row (no hash, blob or filename), drops extraction runs and releases the blobs for the next sweep. `derived=keep` keeps confirmed lab results, which copy the versions and evidence they need; `derived=delete` removes them too (also later, on the tombstone). The audit event holds counts only. Old backups, and PostgreSQL pages not yet vacuumed, keep the wrapped key until they rotate out.
- Metadata: sha256 (a re-upload of the same content links to the live document), size, encrypted original filename, `uploaded_at`, `retention_until`, status `uploaded → extracting → needs_review → confirmed | deleted`.

## Extraction provider interface

```go
type Extractor interface {
    ID() string        // "fake" | "gemini" | "openai" | "openai_compatible"
    External() bool    // true ⇒ document leaves the host
    Extract(ctx context.Context, req ExtractRequest) (ExtractResponse, error)
}
// ExtractRequest: PDF, Pages, Schema (schemas/lab-extraction.v1.json), Prompt (prompts/lab-extraction/v1.md), Model, Hints
// ExtractResponse: Raw (stored encrypted), Rows, DocMeta (lab, dates), ModelID, Usage, Warnings
```

| Provider | Notes |
| --- | --- |
| `fake` | Deterministic, keyed by PDF sha256 or generator ground truth. Used in CI and demos. |
| `gemini`, `openai` | Native PDF input plus schema-constrained output. Model names are configuration. |
| `openai_compatible` | Base URL + model for local or self-hosted inference (privacy option) |

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

- External providers are off by default. Enabling one requires an API-key secret file and `documents.external_ai.<provider>.enabled=true`. Each extraction also requires `consent {provider, model, acknowledged_at}` matching the configuration. Missing consent → `409 consent_required`; a disabled provider → `403`.
- Stored: prompt and schema versions, model id, provider request id, timestamps. Never logged: keys, document bytes, responses.
- PDF text is untrusted input (prompt injection). The model has no tools, output must match the schema, and a human reviews everything.
- Retention: owner settings `documents.retention_days` (default keep) and `documents.delete_original_after_confirmation`, set through `documents.SetPolicy`, which applies the period to every live document's `retention_until`. The daily `document_retention` job deletes due originals with `derived=keep`. Deletion modes: `derived=keep|delete`.
