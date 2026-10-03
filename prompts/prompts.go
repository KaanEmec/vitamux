// Package prompts embeds the versioned extraction prompts so the binary carries the exact
// text that was reviewed (prompts/lab-extraction/REVIEW.md).
package prompts

import _ "embed"

// LabExtractionV1 is prompts/lab-extraction/v1.md (documents.PromptVersion).
//
//go:embed lab-extraction/v1.md
var LabExtractionV1 string
