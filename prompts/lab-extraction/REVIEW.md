# Prompt review checklist

Every prompt version in this directory is reviewed before use. A new version is a new file (`v2.md`), never an edit of a used one. Rules: [CLAUDE.md#blood-tests](../../CLAUDE.md#blood-tests), [lab-documents.md](../../docs/architecture/lab-documents.md).

## v1 — signed off 2026-10-03

- [x] Transcription only: says to copy what is printed and not to interpret results, add flags, or advise.
- [x] No diagnosis, judgement, or advice words in the prompt or in the schema descriptions sent with it. Checked by `TestPromptHasNoInterpretiveLanguage` (`internal/documents/prompt_test.go`); its word list is extended when a review finds a new one.
- [x] No computing or converting: no unit conversion, calculated ratios, or ranges and units from memory.
- [x] Unreadable cells become null with an `unreadable_*` warning; never guessed. Every warning code is explained (`TestPromptNamesItsVersions`).
- [x] Patient names, identifiers, and dates of birth are not transcribed.
- [x] Document text is treated as data; instructions inside it are ignored.
- [x] Names its version (`lab-extraction/v1`) and schema (`vitamux.lab.extraction/1`, `schemas/lab-extraction.v1.json`).
- [x] Ambiguous day and month order leaves the ISO date null and keeps the printed text.
