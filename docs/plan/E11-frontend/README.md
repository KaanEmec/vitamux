# E11 Configurator frontend

Release: MVP · Depends on: E10, G2 · [Plan index](../README.md)
Read first: [frontend#navigation](../../architecture/frontend.md#navigation), [frontend#rule-builder](../../architecture/frontend.md#rule-builder)

**Objective:** A lean SPA for dashboard, connections, data provenance, rules, overrides and settings (lab UI in E12, device UI in E15).

**Outputs:** SvelteKit sections Today, Connections, Data, Rules, Settings; Playwright suite.

## Acceptance
- [x] E2E covers the critical flows (48 stubbed-API specs plus a real-stack smoke).
- [x] axe: no serious violations (0 serious or critical on every section page, light and dark; [J11.6](J11.6-ui-quality.md)).
- [x] Initial JS ≤ 300 KiB gzip (largest route 78.5 KiB, typical 49 to 54 KiB).

## Jobs
| Job | Title | Depends on | Gate |
| --- | --- | --- | --- |
| [J11.1](J11.1-shell-auth.md) | App shell and auth | J10.1, J03.2 | None |
| [J11.2](J11.2-today-connections.md) | Today dashboard and connections | J11.1, J10.3, J10.4, J10.5 | None |
| [J11.3](J11.3-data-provenance.md) | Data views and provenance drilldown | J11.1, J10.3, J10.4 | None |
| [J11.4](J11.4-rules-ui.md) | Rules catalogue and guided builder | J11.1, J10.3, J10.4 | None |
| [J11.5](J11.5-settings.md) | Settings | J11.1, J10.4, J10.5, J10.6 | None |
| [J11.6](J11.6-ui-quality.md) | Frontend quality gates | J11.2, J11.3, J11.4, J11.5 | None |
