# E12 Blood-test documents

Release: MVP · Depends on: E03, E05, E06, E10 · [Plan index](../README.md)
Read first: [lab-documents#storage](../../architecture/lab-documents.md#storage), [lab-documents#extraction-provider-interface](../../architecture/lab-documents.md#extraction-provider-interface), [lab-documents#privacy-controls](../../architecture/lab-documents.md#privacy-controls)

**Objective:** Secure PDF upload, provider-neutral extraction with explicit consent, mandatory review, confirmed lab results with provenance and no interpretation.

**Outputs:** `internal/documents`, extractors (fake, Gemini, OpenAI, OpenAI-compatible), validation, review workflow, analyte catalogue, UI, synthetic lab PDFs.

## Acceptance
- Fake provider: upload → extract → review → confirm works end to end.
- External providers refuse without enablement and per-request consent.
- Deletion crypto-shreds.
- No interpretive text in prompts, outputs or UI copy.

## Parallelism
- Runs in parallel with E08–E11 once E03, E05, E06 exist; J12.6 waits for J11.1.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J12.1](J12.1-document-storage.md) | Document schema and storage | J05.2, J03.1, J10.1 | None |
| [J12.2](J12.2-extraction-contract.md) | Extraction contract, prompt and synthetic PDFs | J04.1 | None |
| [J12.3](J12.3-providers-consent.md) | Extraction providers and consent (ADR-013) | J12.1, J12.2, J06.1 | None |
| [J12.4](J12.4-validation-review.md) | Validation and review API | J12.3, J03.4 | None |
| [J12.5](J12.5-analytes.md) | Analyte catalogue and conversions | J12.1 | None |
| [J12.6](J12.6-lab-ui.md) | Review and results UI | J11.1, J12.4, J12.5 | None |
