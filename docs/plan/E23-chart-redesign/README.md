# E23 Charts and metric visualisation redesign (v0.3.0)

Release: v0.3.0 · Status: in progress · Depends on: E21 · [Plan index](../README.md)
Read first: [frontend#design-system](../../architecture/frontend.md#design-system), [frontend#chart-grammar](../../architecture/frontend.md#chart-grammar), [ADR-0020](../../adr/0020-chart-kit.md)

**Objective:** Make the charts and metric screens the best part of the panel. Every metric gets its own hue and icon tile. Charts are richer and interactive: a crosshair tooltip, a period switch that animates, stat tiles that drive the chart, a brush navigator, and per-source overlays. The data rules stay the same: values are explainable, gaps stay gaps, and nothing is graded.

Owner decisions (2026-10-04):
- Charts move to **LayerChart** (Svelte 5, D3-based). This supersedes [ADR-0020](../../adr/0020-chart-kit.md) and its 40 KiB chart budget; [J23.2](J23.2-layerchart-adr.md) records the new budget.
- Ships as **v0.3.0**, ahead of the iOS app ([E22](../E22-ios-app/README.md), v0.4.0), whose chart kit (J22.6) can mirror the same chart language.
- **Dark first.** The light theme keeps working from the same tokens.
- Inspiration: dark cards, tinted metric icon tiles, period pills, stat tiles that switch the chart below them, and expandable day rows.
- Neutral as before: hues name a metric, source or data state, never good or bad. No judgement labels (for example no BMI classes).

**Outputs:**
- design tokens v3 (metric hues, icon tiles, dark-first surfaces) and an ADR for LayerChart;
- the chart kit rebuilt on LayerChart, with the same public components and grammar plus new ones;
- a redesigned dashboard, metric detail, Explore, sleep, body and blood-pressure views, working at 390 px;
- the additive API fields the new views need, and nothing else;
- the v0.3.0 release.

## Acceptance
- **Design:** the screens match the approved canvas (chart system, dashboard, phone dashboard, metric detail, sleep, body and blood pressure). Every metric shows its hue and icon tile; every chart uses the shared source chips and status markers.
- **Interaction:** every x/y chart has a crosshair tooltip with value, status and source, and the tooltip opens the existing explanation, override and raw-record actions. Period changes animate, except under reduced motion. Touch can scrub through points.
- **Dashboard:** the selected stat tile drives the hero chart (7D/30D/90D/1Y); a last-night card; pinned cards with a sparkline and a neutral delta. The existing edit mode and server-stored layout still work.
- **Metric detail:** a stats header, source overlay toggles, fallback markers, shaded gaps, a source-per-window strip, a brush navigator, a distribution, a values table and the rule lens with a draft preview.
- **Unchanged rules:** the chart grammar still comes from the catalogue, with no per-metric code. Gaps are never drawn as zero. Copy stays neutral.
- **Quality:** each chart keeps one tab stop with arrow keys, Home/End and a "Show as a table" fallback. There are no serious axe violations in either theme or at 375 px. A 14,400-point day renders in under 500 ms. Every route stays within its JS budget, and the chart chunk stays within the J23.2 budget.

## Parallelism
- J23.1, J23.2 and J23.5 can start now.
- J23.3 follows J23.1, and J23.4 follows J23.2 and J23.3.
- Once J23.4 lands, J23.6, J23.7 and J23.8 run in parallel.
- J23.10 and J23.11 run in parallel after J23.6.
- J23.11 restyles the controls on every page; layouts stay.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J23.1](J23.1-design-spec.md) | Chart language and design spec | None | None |
| [J23.2](J23.2-layerchart-adr.md) | LayerChart ADR (supersedes ADR-0020) | None | None |
| [J23.3](J23.3-tokens-v3.md) | Design tokens v3 and metric tiles | J23.1 | None |
| [J23.4](J23.4-chart-kit.md) | Chart kit on LayerChart | J23.2, J23.3 | None |
| [J23.5](J23.5-api-fields.md) | Additive API fields for the new views | None | None |
| [J23.6](J23.6-dashboard.md) | Dashboard redesign | J23.4, J23.5 | None |
| [J23.7](J23.7-metric-detail-explore.md) | Metric detail and Explore | J23.4, J23.5 | None |
| [J23.8](J23.8-sleep-body-views.md) | Sleep, body and blood-pressure views | J23.4, J23.5 | None |
| [J23.9](J23.9-quality-release.md) | Quality gates, docs and v0.3.0 | J23.6–J23.8, J23.10, J23.11 | G9 |
| [J23.10](J23.10-dismiss-alerts.md) | Dismissible dashboard alerts | J23.6 | None |
| [J23.11](J23.11-ux-pass.md) | Controls and overall UX pass | J23.3 | None |

## Out of scope
- New layouts for Connections, Rules, Lab or Settings (J23.11 restyles their controls only).
- Scores computed by Vitamux, goals, insights, correlations or judgement labels.
- New metrics, connectors or resolution behaviour.
- Free-form widget grids and unit switching.
