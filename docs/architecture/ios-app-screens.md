# iOS app screens and deep links

Wireframe-level map for [E22](../plan/E22-ios-app/README.md): which screens, sheets and links each tab has, and the panel route each mirrors. No visual design ([J22.24](../plan/E22-ios-app/J22.24-design-pass.md)). Decisions: [ios-app](ios-app.md#navigation), [ADR-0023](../adr/0023-ios-app.md).

## Rules

- A deep link is `vitamux://` plus the panel path and query, so one parser serves links, widgets, notifications and the OAuth return. The panel root `/` is `vitamux://dashboard`.
- Every link parses into one `Route` case; an unknown path opens the tab root. A link that arrives while signed out or locked opens after sign-in or unlock, as the panel's `?next=`.
- Sheets have no link of their own; they open from their screen.
- Search (the panel's ⌘K palette) is a toolbar button on every tab root that opens a search sheet over sections, settings pages, connections, metrics and rules.

## Outside the tabs

| Screen or sheet | Panel | Job |
| --- | --- | --- |
| Server step: URL field or pairing-QR scan, version check | none (the panel is served by its server) | J22.5 |
| Sign-in: password, then TOTP or recovery code; "session expired" | `/login?reason=` | J22.5 |
| App lock: Face ID or passcode | none | J22.5 |
| Sign-out sheet with "also unpair this iPhone" | shell sign-out | J22.5 |

## Tabs

| Tab | Screen · *sheet* | Panel route | Deep link | Job |
| --- | --- | --- | --- | --- |
| Dashboard | Dashboard: day picker, alerts, cards, edit mode | `/?date=` | `vitamux://dashboard?date=2026-01-31` | J22.7 |
| | *Add metric* | dialog | | J22.7 |
| Explore | Inventory: filters, pins | `/explore` | `vitamux://explore` | J22.8 |
| | Metric detail: range, zoom, sources, coverage, stats, values; Day range | `/explore/{metric}?range=&end=` | `vitamux://explore/heart_rate_resting?range=3M` | J22.8, J22.26 |
| | *Point panel*: provenance, overrides (exclude, force, set value) | dialog | | J22.8 |
| | *Rule lens*: draft overlay, save, activate, revert | sheet | | J22.10 |
| | All-sources day: inputs, overrides list, revoke | `/explore/{metric}/day/{date}` | `vitamux://explore/heart_rate/day/2026-01-31` | J22.8 |
| | Sleep, Blood pressure, Body composition, Workouts | `/explore/sleep` and siblings | `vitamux://explore/sleep` | J22.9 |
| | Events | `/explore/events?code=` | `vitamux://explore/events?code=…` | J22.9 |
| | Apple Watch views: ECG list and strip, RR plot, rings, State of Mind, workout route and segments | new in both clients | `vitamux://explore/ecg`, `…/ecg/{id}`, `…/beats?date=`, `…/activity-rings`, `…/state-of-mind`, `…/workouts/{id}` | J22.18 |
| Sources | Sources: "This iPhone" card, connections, run strips, banners | `/connections?connected=&auth_error=&provider=&removed=` | `vitamux://connections?connected=withings` (OAuth return) | J22.11 |
| | *Connect a source*: setup states, Withings app wizard, sidecar card, prompt steps, OAuth in `ASWebAuthenticationSession` | dialog | | J22.11 |
| | Connection detail: overview, streams, devices, backfills, history, settings | `/connections/{id}?tab=` | `vitamux://connections/{id}?tab=backfills` | J22.11 |
| | *Start backfill*, *merge device*, *delete connection* | dialogs | | J22.11 |
| Lab | Documents | `/lab` | `vitamux://lab` | J22.12 |
| | *Add document*: camera scan, Files, share sheet; *extract with consent* | dialogs | | J22.12 |
| | Review: PDF with row outline, accept or reject, confirm, unconfirm | `/lab/documents/{id}` | `vitamux://lab/documents/{id}` | J22.12 |
| | *Row editor* | panel side pane | | J22.12 |
| | Results by analyte | `/lab/results` | `vitamux://lab/results` | J22.12 |
| | Analyte history | `/lab/analytes/{code}` | `vitamux://lab/analytes/{code}` | J22.12 |
| More | Rules catalogue, coverage heatmap | `/rules` | `vitamux://rules` | J22.10 |
| | Rule page: versions, diff, activate | `/rules/{metric}?saved=` | `vitamux://rules/{metric}` | J22.10 |
| | Rule builder, one step per screen | `/rules/new` | `vitamux://rules/new` | J22.10 |
| | Settings › Profile, timezone periods | `/settings` | `vitamux://settings` | J22.13 |
| | Settings › Sources, Devices, AI providers, API keys, Security, Retention, Backups, System status | `/settings/{page}` (`sources`, `devices`, `ai`, `api-keys`, `security`, `retention`, `backups`, `system`) | `vitamux://settings/{page}` | J22.13 (system: worked example in J22.1) |
| | *API key shown once*, *recovery codes shown once*, *TOTP enrol* | dialogs | | J22.13 |
| | Settings › App: theme, app lock, notifications, cache, widget redaction | none (app only) | `vitamux://settings/app` | J22.13 |
| | Apple Health: pairing, groups, per-type status, source filter, Apple Watch card, privacy | Bridge app; source filter also in `/settings/devices` | `vitamux://apple-health` | J22.14, J22.25, J22.18 |
