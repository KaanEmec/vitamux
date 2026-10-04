# ADR-0010 SvelteKit static SPA embedded in the binary

Status: Accepted, amended by [ADR-0020](0020-chart-kit.md) (charts) · Date: 2026-10-03 · Deciders: owner (G0 approval)

## Context
The configurator needs real interactivity (rule builder, chart overlays, PDF evidence review) but must not add a container or runtime. See [frontend.md](../architecture/frontend.md).

## Decision
Use SvelteKit with `adapter-static`, SSR off, and TypeScript, plus a generated OpenAPI client. Assets are built in CI and embedded via `go:embed`; `vitamux` serves them with an SPA fallback. Libraries are limited to `uPlot` and lazy `pdf.js` (charts: since [ADR-0020](0020-chart-kit.md), an own SVG kit replaces `uPlot`). No component framework. Strict CSP with no inline scripts.

## Alternatives considered
- **Go templates + htmx**: no JS build, but the rule builder, charts, and PDF review would still need substantial hand-written JS.
- **React + Vite**: larger ecosystem, but heavier bundles and more churn.

## Consequences
- Node is needed at build time only.
- Initial JS budget ≤ 300 KiB gzip, enforced in J11.6.
