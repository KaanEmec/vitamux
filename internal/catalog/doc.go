// Package catalog is the code-owned source of truth for metrics and units (docs/architecture/metric-catalog.md).
// The seed migrations and docs/metrics.md are generated from it; TestGeneratedFilesUpToDate fails on drift.
//
// Each metric and unit carries a seed marker (Since; the v1 seed 00010 when 0) and the generator
// writes one migration per marker (SeedFiles), so a released seed file never changes. A code
// added later (a connector job, E15) takes a new marker and a new entry in seedFiles.
package catalog

//go:generate go run ./gen
