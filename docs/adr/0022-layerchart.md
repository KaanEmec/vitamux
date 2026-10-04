# ADR-0022 LayerChart for the chart kit

Status: Accepted · Date: 2026-10-04 · Deciders: owner · Supersedes [ADR-0020](0020-chart-kit.md)

## Context
[E23](../plan/E23-chart-redesign/README.md) makes charts richer: a crosshair tooltip, animated period switches, a brush navigator, per-source overlays and more chart types. ADR-0020 chose a hand-written SVG kit for the E21 charts. The owner now prefers a maintained library for the redesign and accepts a larger lazy chunk. The kit's product rules stay ours: status markers, source colours, tokens, one keyboard model, the table fallback and the `vx-chart-render` mark ([frontend#chart-grammar](../architecture/frontend.md#chart-grammar)).

A spike drew the HRV detail chart on LayerChart 2.5.1 (Svelte 5.57, Vite 8, Chromium, the production CSP `style-src 'self'`): a line, a range band, two dashed overlay lines, a crosshair tooltip, a brush navigator that sets the main x domain, and the table fallback. It used the primitives from `layerchart/svg` and applied the min/max-per-half-pixel-column reduction of `scale.ts` first (mount to next frame, median of 9 cold loads):

| Measure | LayerChart (spike) | Own SVG kit (ADR-0020) |
| --- | --- | --- |
| Lazy chunk, gzip (everything one chart import loads) | 115 KiB | 6.7 KiB |
| 14,400 points (at most 3,400 points drawn after reduction) | 53 ms | 14 ms |
| 50,000 points (same reduction) | 56 ms | 13 ms |
| CSP violations caused by the chart | 0 | 0 |
| Initial JS | unchanged (lazy only) | unchanged |

The single `style-src-attr` violation in the page is SvelteKit's route announcer, present without any chart and already tolerated by the E2E harness. LayerChart sets its inline geometry through CSSOM.

Keyboard model on top: a wrapper keeps one tab stop (LayerChart renders nothing focusable); arrows, Home and End move the tooltip through `context.tooltip.show({ data, point })` and `hide()`, and the highlight follows; `ChartTable` is unchanged.

## Decision
Rebuild the kit in `web/src/lib/charts/` on LayerChart, keeping the public components and grammar. Import only from `layerchart/svg` (smaller than the layer-agnostic root export), and keep a thin wrapper that owns the keyboard model, status markers, source colours, tooltip content and the table fallback. Pages keep loading the kit with `{#await import(…)}`. LayerChart is the only new direct dependency; its peer is Svelte. Reduce dense series with `scale.ts` before they reach LayerChart. Under `prefers-reduced-motion` the wrapper turns chart motion off.

Budget: the chart code behind one lazy import stays ≤ 150 KiB gzip (spike: 115 KiB), enforced by `npm run budget` (`scripts/bundle-budget.mjs`) as the biggest set of chunks one chart import loads, minus the initial JS. The initial-JS budget (300 KiB) is unchanged.

## Alternatives considered
- **Keep the own SVG kit (ADR-0020)**: 20× smaller and faster, but every new chart type, the tooltip, brush and transitions are ours to write and maintain. The owner chose the library.
- **ECharts (modular)**: 194 KiB and its own theming and accessibility model (ADR-0020 spike).
- **uPlot**: fast and small, but a second drawing model beside the SVG charts.

## Consequences
- About 57 transitive packages (MIT, ISC, BSD-3-Clause, Unlicense; D3 modules and layout helpers) enter the lockfile; only a subset reaches the lazy chunk. Track LayerChart releases and re-run the budget on upgrades.
- Chart code is idiomatic Svelte 5, with tween motion and a brush context for the navigator, but LayerChart's default tooltip and styles are replaced by our tokens and components.
- A 14,400-point day still renders well under 500 ms ([E23](../plan/E23-chart-redesign/README.md) acceptance); reduction stays our responsibility, because LayerChart draws every row it is given.
- Revisit this ADR if a view must draw more than about 200,000 points, or if the lazy chunk outgrows the budget.
