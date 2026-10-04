# Frontend

## Technology

- SvelteKit with `adapter-static`, SSR off, TypeScript. Built in CI and embedded in `vitamux` with `go:embed`; Node is needed only at build time.
- Generated OpenAPI client; an own SVG chart kit in `lib/charts/`, lazy-loaded ([ADR-0020](../adr/0020-chart-kit.md); a 14,400-point day renders well under 500 ms); `pdf.js` lazy-loaded for lab review (dynamic import in `lib/lab/PdfViewer.svelte`, no WebAssembly; the row outline is an SVG over the canvas). Geist and Geist Mono are self-hosted (`@fontsource-variable/*`). No component framework.
- Budget: ≤ 300 KiB gzip initial JS, excluding the lazy chunks; the chart code behind one lazy import ≤ 40 KiB gzip. Strict CSP (no inline scripts or style attributes; CSSOM through `style:` directives is fine).
- Accessibility: keyboard navigation, labelled controls, status shown by shape and colour (never colour alone). Responsive, but not a mobile app.

Alternatives rejected: htmx (the rule builder, charts, and PDF review need real JS anyway) and React (heavier, more churn).

## Code layout

- Imports use `#lib/…` (SvelteKit 3 subpath imports) with explicit `.ts` extensions; relative imports inside `src/lib`.
- Sections are routes under `src/routes/(app)/` (listed in `src/lib/nav.ts`), whose layout is the shell and the session guard; `/login` sits outside it. A page sets `<title>X · Vitamux</title>` and one `<h1>`.
- API calls go through `api` in `src/lib/api/client.ts` (`openapi-fetch` over the generated `schema.d.ts`). It adds `X-CSRF-Token` on mutations, sends any other 401 to `/login?next=…&reason=expired`, and turns every failure into a `Problem`. Show it with `ProblemAlert`; map field errors to inputs with `fieldErrors` and `TextField`.
- Styling follows the [design system](#design-system): tokens in `src/lib/styles/tokens.css`, the few shared classes in `base.css`, primitives in `src/lib/ui/`. No raw colours or sizes, no inline `style` attributes (the CSP blocks them).
- Charts come from `src/lib/charts/` through a dynamic import, so they stay out of the route's initial JS: `{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}`. Pages never draw charts by hand.
- E2E: `npm run test:e2e` builds and runs Playwright (`web/e2e`) against `vite preview`. The API is stubbed with `page.route` (`e2e/fake-api.ts`), and CSP violations fail the test. `e2e/a11y.spec.ts` runs axe over every section page (serious or critical fails). `make test-e2e-stack` runs one smoke against a real server on a throwaway database (`scripts/e2e-stack.sh`, `web/e2e-stack`).
- Gates in CI (`web` job): `npm run check` (svelte-check, warnings fail), eslint, `npm run budget` (`scripts/bundle-budget.mjs`, per-route initial JS), Playwright.

## Navigation

Every section uses the design system below ([E21](../plan/E21-visualisation/README.md), v0.2.0).

- **Shell** (`(app)/+layout.svelte`, `lib/nav.ts`, `lib/shell/`): a collapsible icon sidebar with Dashboard (`/`), Explore (`/explore`, which also owns `/data`), Connections, Rules, Lab results and Settings, and the signed-in owner. The top bar has the ⌘K / Ctrl+K command palette (sections, Settings pages, connections, metrics from `GET /metrics` and their rules), a sync-status pill (latest successful sync, or how many connections need attention), the theme toggle and sign-out. Below 48rem a bottom bar shows Dashboard, Explore and Sources, and "More" opens the full menu.
- **Theme**: system, light or dark, a per-browser preference (`lib/prefs.svelte.ts`, `localStorage`, `<html data-theme>`), as is the collapsed sidebar.
- A metric opens at `metricHref()` in `lib/nav.ts`; `lib/explore/links.ts` maps inventory items to their view. Old `/data/*` URLs redirect to Explore.

Sections:

1. **Dashboard** (`/`, `lib/dashboard/`): a stored layout (`GET/PUT /settings/dashboard`) of metric cards from `GET /resolved/summary`: value, neutral delta against the 30-day mean, sparkline, source chips, status. Edit mode reorders (drag or buttons), sizes S/M/L, hides and adds cards; `?date=` shows a past day. Alerts (reauth, drift, stale backup) and connection health stay on top.
2. **Explore** (`/explore`, `lib/explore/`): everything stored (`GET /inventory`) by section with provider, device and origin filters, and pinning to the Dashboard. A metric page (`/explore/[metric]`) draws the view `chartFor()` picks: range picker and brush, status markers, 30-day band, the per-source overlay (`GET /sources/series`), coverage strip, stats and values table; long ranges use `GET /resolved/trend`. A point opens its explanation, provenance and override actions; `day/[date]` is the all-sources drilldown. The **rule lens** (`lib/rules/RuleLens.svelte`) edits the rule beside the chart and overlays the draft from `POST /resolution/preview` before saving. Specialised views: `sleep`, `blood-pressure`, `body-composition`, `workouts`, `events`, and `/lab/analytes/[code]` (printed range only).
3. **Connections**: provider cards with health, last sync, a 14-day run strip and the next action; then detail tabs (`?tab=`) Overview (sync timeline), Streams, Devices, Backfills, History, Settings.
   - **Devices** (`lib/connections/DevicesTab.svelte`, [J20.7](../plan/E20-guided-setup/J20.7-devices.md)): the provider's devices with records from the connection, with type select, name field (saved on change) and records; "Merge into…" opens a confirm dialog naming what moves and stays irreversible in the UI. Merged devices are listed below the table ([source devices](api.md#source-devices)). **Connect a source** (`lib/setup/SourceSetup.svelte`, [E20](../plan/E20-guided-setup/README.md)) is a dialog, and the page itself while nothing is connected (the Dashboard's empty state links to it): every provider of `GET /providers` as a card with its [setup state](../adr/0021-source-setup.md) and one next action.
   - `needs_app_credentials`: the app wizard (`AppWizard`): create the app with the callback URL to copy, paste the client id and secret (saved, then verified), connect by OAuth. A refused OAuth return (`?auth_error=exchange_failed&provider=`) offers to review it.
   - `needs_sidecar`: the card shows the enable line for Compose or Coolify and **Check again** (`SidecarSteps`, probe); `needs_public_url`: what to change. Neither can be chosen.
   - `ready` or `connected`: OAuth redirect, or the sign-in and MFA prompts (`AuthPrompt`, autocomplete hints, values cleared once sent), whose failures read in plain language (`setup.ts` `signInError`: wrong password or code, rate limit with its wait, sidecar gone).
4. **Rules**: catalogue cards with the rule as a sentence (`lib/rules/sentence.ts`), source order and coverage heatmap; the metric page with a version timeline, side-by-side diff and the rule lens; the builder.
5. **Lab results**: upload with the consent dialog naming provider and model; review as a split view (PDF page with the row outlined beside the row list and editor; Enter accepts, J/K move, E edits); results by analyte linking to their trend ([lab-documents.md](lab-documents.md)).
6. **Settings**: grouped navigation (You, Sources, Access, Data, System), one card pattern (`lib/settings/Card.svelte`) for profile and time zones, sources (app credentials: set, replace, remove, read-only when set by the environment; sidecars: list, add with the secret shown once, remove), devices (pairing QR, origins), API keys, AI providers, retention, backups and export, security and system status.

## Rule builder

The builder (`src/routes/(app)/rules/new`) is a stepper with a live preview sidebar; it follows [resolution.md](resolution.md) in five steps and starts from the rule in effect, a saved version, or an empty rule:

1. **Metric**: from the catalogue (`GET /rules`, plus `GET /metrics` codes without a rule).
2. **Sources**: ordered groups of selectors (ORed lists of ANDed conditions) with values seen in the data and the active rules as suggestions and one-click chips for origins, device types and relayed or direct (`GET /origins`, `GET /source-devices`); up/down buttons to reorder; exclusions; a relay-exclusion suggestion. A "Choose a source or device" picker above the raw fields offers named choices built from `GET /source-devices` and `GET /providers` (Apple Health, Apple Watch, iPhone, a provider or brand, a model, an owner-named device) and fills the selector; built-in group ids read as names (`apple_watch` is Apple Watch).
3. **Strategy**: plain-language cards ("Use the first source with data", "Average the sources", "Take the highest", …) and within-group options. A sum (across or within groups) cannot be saved until the duplicate-risk checkbox adds `cross_source_sum_duplicate_risk`.
4. **Window and quality**: window (the server checks it against the metric), coverage, plausible range, flags, staleness, wear, sleep alignment, `follow` and `compose`. Extensions the form does not edit (`contexts`) are kept.
5. **Review**: the rule JSON, its diff against the rule in effect, and the last 14 days for the draft versus the active rule with a per-day diff and explanations (`POST /resolution/preview`; "preview unavailable" on 404/503).

Saving (`POST /rules/{metric}/versions`) creates a new version; server field errors return to their step and input. The metric page (`rules/[metric]`) lists versions with a field diff between any two and activates any of them. The catalogue and metric pages show the 90-day `GET /coverage` heatmap when the endpoint answers; the metric page filters it by origin app.

## Design system

Design system v2 ("midnight teal"): calm surfaces, one accent, numbers in tabular figures. The approved canvas is the reference; the code is the source of truth.

- **Tokens** (`tokens.css`): every colour is `light-dark(light, dark)`, so `color-scheme` (system or `data-theme`) picks the theme. Groups: `--color-*` (surfaces, text, accent, link, focus, feedback), `--status-*`, `--src-*`, `--stage-*`, `--chart-*`; `--text-2xs`…`--text-display`, `--space-1`…`--space-8` (4–48 px), `--radius-xs|sm|md|lg|pill`, `--control-h`, `--shadow-1|2`, `--focus-ring`. Motion is minimal and off under `prefers-reduced-motion`.
- **Primitives** (`lib/ui/`): `Button`, `Segmented`, `Tabs` (link tabs), `Chip` (source chip), `Badge`, `EmptyState`, `Skeleton`, `CopyValue` (a value with a Copy button), `Icon`/`icons.ts`, `Logo`; `.card`, `.btn`, `.field` in `base.css`; `Modal` (also a side `drawer`), `TextField`, `ProblemAlert`, `ExplainPopover`, `StatusIcon` and `HealthBadge` in `lib/components/`. Add a primitive when a second page needs it.
- **Data status** (`lib/ui/status.ts`, `ResultStatus`): a shape, a colour and a word for each of direct (circle), fallback (diamond), calculated (triangle), overridden (square), partial (half circle) and no data (ring). Status colours describe data state only.
- **Source colours** (`lib/ui/source.ts`): a provider maps to a `.src-*` class that sets `--src`; Apple Health, WHOOP, Withings, Garmin and push/manual are fixed, others take one of three extra colours by a stable hash. Series also differ by dash, so colour is never the only cue.
- **Copy**: neutral words for values ("30-day mean", "vs 90-day mean"), never good or bad, no advice; plain language for errors; lab values, labels, units and ranges "as printed".

## Chart grammar

`chartFor(metric)` in `lib/charts/grammar.ts` picks the view from the catalogue (`GET /metrics`), so no metric has its own chart code:

| Catalogue | View | Kit |
| --- | --- | --- |
| `intensive` | line with min–max band | `TimeSeries` + `band` |
| `additive` | bars per window | `Bars` |
| `latest` | step line with readings | `TimeSeries step` |
| `daily_summary` | line with baseline | `TimeSeries` + `baseline` |
| `sleep_derived` | stage stack, hypnogram | `Bars` (stacks by stage), `Hypnogram` |
| group `bp_*` | dumbbells | `RangeDumbbell` |
| events | timeline lanes | `EventLanes` |
| lab analytes | points with the printed range | `TimeSeries` (`style: 'dots'`) + `band` |

Also in the kit: `Sparkline`, `CoverageStrip`, `RangePicker`, `ChartTooltip`, `ChartTable`. Every x/y chart draws in `ChartFrame`: one tab stop with arrow keys, Home/End and PageUp/PageDown between points (announced politely), Enter to open a point, drag to zoom, a "Show as a table" fallback, and the `vx-chart-render` User Timing measure. A draft series is `style: 'ghost'`; non-direct points carry their status marker.
