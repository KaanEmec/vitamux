// Package schemas embeds the JSON Schemas the binary sends or enforces at runtime.
package schemas

import _ "embed"

// LabExtractionV1 is schemas/lab-extraction.v1.json (documents.ExtractionSchema).
//
//go:embed lab-extraction.v1.json
var LabExtractionV1 []byte

// ConnectorSidecarV1 is schemas/connector-sidecar.v1.json, the messages of the sidecar
// protocol; `vitamux connector-test` validates a sidecar's responses against it.
//
//go:embed connector-sidecar.v1.json
var ConnectorSidecarV1 []byte

// ConnectorTestScenarioV1 is schemas/connector-test-scenario.v1.json.
//
//go:embed connector-test-scenario.v1.json
var ConnectorTestScenarioV1 []byte
