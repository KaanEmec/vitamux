// Package catalog is the code-owned source of truth for metrics and units (docs/architecture/metric-catalog.md).
// The 00010 seed migration and docs/metrics.md are generated from it; TestGeneratedFilesUpToDate fails on drift.
//
// A code added later (a connector job, E15) gets its own migration; the generator only owns the v1 seed.
package catalog

//go:generate go run ./gen
