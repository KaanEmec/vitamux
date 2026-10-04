# E21 Panel redesign, dashboard and data exploration (v0.4.0)

Release: v0.4.0 · Depends on: E11, E12, E15, E20 (its screens are restyled and its first-run card is reused); E18 and E19 for real multi-source checks · [Plan index](../README.md)
Read first: [frontend](../../architecture/frontend.md), [resolution#result-shape](../../architecture/resolution.md#result-shape), [api#owner-endpoints-apiv1](../../architecture/api.md#owner-endpoints-apiv1)

**Objective:** Make the panel a sleek, modern place to look at your data and to change how it is resolved. Every existing screen is redesigned on one design system. A dashboard shows the mainstream metrics with neutral baselines. Explore lists everything Vitamux has stored, and every metric chart can change its own rule in place, with a preview.

Owner decisions (2026-10-04):
- E21 ships as v0.4.0, after E18–E20.
- The dashboard is curated by default; the owner can pin, reorder, resize (S/M/L) and hide cards. The layout is stored on the server.
- A richer, efficient chart library may replace or join uPlot. This amends [ADR-0010](../../adr/0010-sveltekit-static-spa.md) ([J21.2](J21.2-chart-library-adr.md)). The backend stays Go.
- Baselines are neutral: 7-, 30- and 90-day mean, delta, min–max band. No good/bad colouring or advice. Status colours describe data state only.
- The redesign covers every existing screen: shell, Today, Connections, Data, Rules, Lab, Settings and login.

**Outputs:**
- design system v2 and a new app shell;
- a chart kit;
- inventory, events, summary, trend and all-sources range endpoints;
- Dashboard, Explore and metric-detail pages, plus the specialised views;
- the rule lens;
- redesigned Connections, Rules, Lab and Settings;
- the v0.4.0 release.

## Acceptance
- **Redesign:** every page uses design system v2 and the new shell. No page uses raw colours or sizes. Light, dark and a manual theme override work. Every E11, E12, E15 and E20 flow still works: the existing e2e suite passes, with updated selectors where needed. Features and API contracts of existing screens are unchanged.
- **Dashboard:** curated cards with value, neutral delta, sparkline, source chips and status. The layout survives a reload and a second browser. It loads in under 1 s on the synthetic 3-year dataset.
- **Explore:** every catalogue metric, group, event type and lab analyte that has data appears. Each one opens a working view chosen from catalogue metadata (the chart grammar), with no per-metric code.
- **Rule lens:** from any metric chart, the owner can reorder source priority and change the strategy, window or coverage, see the draft overlaid before saving, and then save, activate or revert. Everything goes through the existing rule versions, so it is audited.
- **Explanations:** every plotted point opens its explanation, provenance and the existing override actions.
- **Neutral copy:** baselines are labelled as plain statistics. No copy judges a value.
- **Quality:** no serious axe violations in either theme or at 375 px; charts are keyboard-reachable and have a table fallback. The chart code loads as a lazy chunk, and every route stays within its JS budget.

## Parallelism
- J21.1, J21.2, J21.5 and J21.6 can start now.
- Once J21.3 and J21.4 land, J21.7–J21.12 run in parallel: three streams for new screens, two for the redesign.
- If J21.3 merges before E20's UI jobs (J20.3, J20.5, J20.6), E20 builds on the new kit and nothing is built twice.

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J21.1](J21.1-design-spec.md) | Design system v2, UX spec and chart grammar | None | None |
| [J21.2](J21.2-chart-library-adr.md) | Chart library ADR (amends ADR-0010) | J21.1 | None |
| [J21.3](J21.3-design-system-shell.md) | Design system v2 and app shell | J21.1 | None |
| [J21.4](J21.4-chart-kit.md) | Chart kit | J21.2, J21.3 | None |
| [J21.5](J21.5-inventory-events-api.md) | Inventory and events API | None | None |
| [J21.6](J21.6-summary-trend-api.md) | Summary, trend and all-sources range API | None | None |
| [J21.7](J21.7-dashboard.md) | Dashboard | J21.4, J21.6 | None |
| [J21.8](J21.8-explore.md) | Explore and generic metric detail | J21.4, J21.5, J21.6 | None |
| [J21.9](J21.9-specialised-views.md) | Specialised views | J21.8 | None |
| [J21.10](J21.10-rule-lens.md) | Rule lens | J21.8 | None |
| [J21.11](J21.11-redesign-connections-rules.md) | Redesign Connections and Rules | J21.3, J21.4 | None |
| [J21.12](J21.12-redesign-lab-settings.md) | Redesign Lab and Settings | J21.3, J21.4 | None |
| [J21.13](J21.13-quality-release.md) | Quality gates, docs and v0.4.0 | J21.7–J21.12 | G7 |

## Out of scope
- Goals or targets, scores computed by Vitamux, insights or coaching text, correlations.
- Unit-preference switching.
- A multi-user UI.
- Free-form widget grids.
- New connector features: redesigned screens keep their behaviour.
