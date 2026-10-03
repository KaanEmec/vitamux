// synthetic: true
// Package lab embeds the synthetic lab-report ground truth (fixtures/README.md#lab-reports)
// that the fake extractor answers with, keyed by the PDF sha256 in manifest.json.
package lab

import "embed"

// FS holds manifest.json and lab-NN.json.
//
//go:embed *.json
var FS embed.FS
