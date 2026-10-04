# ADR-0020 Hand-written SVG chart kit, no chart library

Status: Superseded by [ADR-0022](0022-layerchart.md) · Date: 2026-10-04 · Deciders: owner · Amends [ADR-0010](0010-sveltekit-static-spa.md)

## Context
E21 ([J21.2](../plan/E21-visualisation/J21.2-chart-library-adr.md)) needs line + band + markers + a draft series, bars and stacks, range dumbbells, step lines with readings, sparklines, event lanes, brush-to-zoom, a tooltip, keyboard focus per point and a table fallback, under the CSP `style-src 'self'` and the per-route JS budget ([frontend#technology](../architecture/frontend.md#technology)).

A spike rendered the same 1-line time series with each candidate in one page each (Vite build, Chromium, CSP as in production; mount to next frame):

| Candidate | Lazy chunk, gzip | 14,400 points | 50,000 points | CSP violations |
| --- | --- | --- | --- | --- |
| LayerChart 2.5 (`LineChart`, Svelte 5) | 139 KiB | 41 ms | 65 ms (756 KB SVG path, no decimation) | 0 |
| ECharts 6.1 modular (line, bar, custom, tooltip, dataZoom, SVG renderer) | 194 KiB | 39 ms | 48 ms | 0 |
| uPlot 1.6 | 22 KiB | 10 ms | 10 ms | 0 |
| Hand-written SVG, min/max per pixel column | 1 KiB | 14 ms | 13 ms | 0 |

All are MIT or Apache-2.0. The libraries would still need our own status markers, source colours, tokens, keyboard model and table, because those are product rules, not chart features.

## Decision
Draw charts with a small Svelte kit in `web/src/lib/charts/` on plain SVG: shared scales, ticks and paths (`scale.ts`), one frame (`ChartFrame.svelte`) for axes, tooltip, keyboard, brush-to-zoom, table fallback and the `vx-chart-render` mark, and thin components on top. Dense series are reduced to their min and max per half-pixel column before drawing. uPlot is removed; the dense day drilldown uses the same kit. Pages load chart components with `{#await import(…)}`, so the kit is a lazy chunk.

Budget: the chart code reachable from one lazy import stays ≤ 40 KiB gzip (TimeSeries today: 6.7 KiB).

## Alternatives considered
- **LayerChart**: idiomatic Svelte 5, but 20× the size of the kit and no decimation; we would still restyle and wrap every mark.
- **ECharts (modular)**: complete and fast, but 194 KiB and its own theming and accessibility model.
- **uPlot plus hand-written SVG**: two drawing models for one design system; the SVG kit already meets the 14,400-point budget, so uPlot adds nothing.

## Consequences
- No chart dependency to track; theming is CSS custom properties, interaction follows the kit's one keyboard model.
- New views (heatmaps, scatter) are written by us; revisit this ADR (canvas layer, or uPlot for raw drilldowns) if a view must draw more than ~200,000 points or many charts at once become slow.
