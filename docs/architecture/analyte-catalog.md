# Analyte catalogue

Rules for the lab analytes behind the `analytes` and `analyte_aliases` tables ([lab-documents.md](lab-documents.md)). The implemented codes, canonical units, conversion factors, refused units and seeded aliases live in the generated [analytes.md](../analytes.md); the source is [`internal/documents/analytes`](../../internal/documents/analytes). Device and wearable metrics live in [metric-catalog.md](metric-catalog.md).

Adding a code means appending it to `internal/documents/analytes` (ids are positions, so append only) in the job that needs it, with its own migration; `go run ./internal/documents/analytes/gen` owns only the v1 seed. A factor change bumps `analytes.Version`, which every canonical lab value records.

## Rules

- The catalogue only normalizes data. It never interprets values, and it stores no reference ranges: ranges come from the printed report.
- The original label, value text, unit, range and flag are always kept. A canonical value exists **only** when the analyte has a conversion for the printed unit; the factor and offset used are stored with it, so conventions (insulin µIU/mL ×6.00) stay traceable.
- An unknown analyte or unit stays confirmable with its original values only (`ErrUnknownUnit`, no canonical value).
- Codes match `^[a-z][a-z0-9_]*$`. The specimen is part of the code only when the specimen changes the meaning, e.g. `urine_glucose` vs `glucose`.
- A computed ratio or index (HOMA-IR, non-HDL, A/G ratio) is stored only when printed on the report. Vitamux never computes and stores one.
- LOINC is an optional field, filled only from a verified loinc.org lookup, never guessed. None is verified yet, so the column is empty; the tests check format and check digit only.
- Factor = canonical value per source unit. An affine conversion has an offset too (HbA1c: mmol/mol = (% NGSP − 2.15) × 10.929).
- An analyte with **no conversion** between unit systems keeps one code per system (`lpa_molar` nmol/L vs `lpa_mass` mg/dL) and lists the other system as refused: converting it fails with `ErrNoConversion`, never a guess. D-dimer FEU vs DDU works the same within one code.
- Printed units match ignoring case, spaces and parentheses (so `G/L` and `g/L` are not told apart); see [analytes.md](../analytes.md) for the spellings accepted.
- Aliases: the seed maps each analyte's name, code and common labels; a label shared by two analytes (e.g. `Lp(a)`) is not seeded. Owner aliases (`/api/v1/analytes/aliases`) take precedence and only ever produce a suggestion that review must confirm.
