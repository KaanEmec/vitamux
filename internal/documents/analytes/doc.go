// Package analytes is the code-owned lab analyte catalogue and its unit conversions
// (docs/architecture/analyte-catalog.md). The seed migration (*_analytes.sql) and
// docs/analytes.md are generated from it; TestGeneratedFilesUpToDate fails on drift.
//
// The catalogue only normalizes: it holds no reference ranges and never interprets a value.
// A canonical value exists only when the analyte has a conversion for the printed unit;
// callers always keep the printed label, value text and unit beside it.
package analytes

//go:generate go run ./gen
