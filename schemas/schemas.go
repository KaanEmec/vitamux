// Package schemas embeds the JSON Schemas the binary sends or enforces at runtime.
package schemas

import _ "embed"

// LabExtractionV1 is schemas/lab-extraction.v1.json (documents.ExtractionSchema).
//
//go:embed lab-extraction.v1.json
var LabExtractionV1 []byte
