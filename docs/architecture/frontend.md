# Frontend

## Technology

- SvelteKit with `adapter-static`, SSR off, TypeScript. Built in CI and embedded in `vitamux` with `go:embed`; Node is needed only at build time.
- Generated OpenAPI client; a chart kit in `lib/charts/` on LayerChart, lazy-loaded ([ADR-0022](../adr/0022-layerchart.md); a 14,400-point day renders well under 500 ms); `pdf.js` lazy-loaded for lab review (dynamic import in `lib/lab/PdfViewer.svelte`, no WebAssembly; the row outline is an SVG over the canvas). Geist and Geist Mono are self-hosted (`@fontsource-variable/*`). No component framework.
- Budget: ≤ 300 KiB gzip initial JS, excluding the lazy chunks; the chart code behind one lazy import ≤ 150 KiB gzip (`npm run budget`). Strict CSP (no inline scripts or style attributes; CSSOM through `style:` directives is fine).
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

1. **Dashboard** (`/`, `lib/dashboard/`): hero stat tiles (the layout's `hero`, up to four) whose selected tile drives the hero chart: 7D/30D/90D from `GET /resolved/series` (one value per local day), 1Y as weekly means from `GET /resolved/trend`, with the grammar's view, a mean line, the range and a neutral delta from the summary's `comparisons`; beside it last night (`GET /resolved/sleep`, hypnogram from `GET /sleep`). Below, a stored layout (`GET/PUT /settings/dashboard`) of pinned cards from `GET /resolved/summary`: value, neutral delta against the 30-day mean, sparkline, source chip, status. Edit mode picks the hero tiles, reorders (drag or buttons), sizes S/M/L, hides and adds cards; `?date=` shows a past day. Alerts (reauth, drift, stale backup) and connection health stay on top. On a phone the tiles scroll sideways and the cards stack.
2. **Explore** (`/explore`, `lib/explore/`): everything stored (`GET /inventory`) by section (rows: metric tile, sparkline, latest value, source chips, days with data, pin) with provider, device and origin filters, and pinning to the Dashboard. A metric page (`/explore/[metric]`) draws the view `chartFor()` picks under a stats header (latest, period mean and range against the period before from `GET /resolved/summary?compare=true`, coverage): range picker, "Compare previous" overlay, source overlay toggles per provider (`GET /sources/series`), status markers, a 7-day range band and the period mean, the source per day (from the inputs of `GET /resolved/daily`), the brush, then the distribution and the values table (status, source, rule version); long ranges use `GET /resolved/trend`. A point opens its explanation, provenance and override actions; `day/[date]` is the all-sources drilldown. The **rule lens** (`lib/rules/RuleLens.svelte`) is the side panel (stacked under the chart on small screens): it edits the rule with the days each source supplied, previews the draft from `POST /resolution/preview` as a mini chart, a change summary and a ghost series on the chart, saves, and reverts from its history. Specialised views: `sleep`, `blood-pressure`, `body-composition`, `workouts`, `events`, and `/lab/analytes/[code]` (printed range only).
3. **Connections**: provider cards with health, last sync, a 14-day run strip and the next action; then detail tabs (`?tab=`) Overview (sync timeline), Streams, Backfills, History, Settings. **Connect a source** (`lib/setup/SourceSetup.svelte`, [E20](../plan/E20-guided-setup/README.md)) is a dialog, and the page itself while nothing is connected (the Dashboard's empty state links to it): every provider of `GET /providers` as a card with its [setup state](../adr/0021-source-setup.md) and one next action.
   - `needs_app_credentials`: the app wizard (`AppWizard`): create the app with the callback URL to copy, paste the client id and secret (saved, then verified), connect by OAuth. A refused OAuth return (`?auth_error=exchange_failed&provider=`) offers to review it.
   - `needs_sidecar`: the card shows the enable line for Compose or Coolify and **Check again** (`SidecarSteps`, probe); `needs_public_url`: what to change. Neither can be chosen.
   - `ready` or `connected`: OAuth redirect, or the sign-in and MFA prompts (`AuthPrompt`, autocomplete hints, values cleared once sent), whose failures read in plain language (`setup.ts` `signInError`: wrong password or code, rate limit with its wait, sidecar gone).
4. **Rules**: catalogue cards with the rule as a sentence (`lib/rules/sentence.ts`), source order and coverage heatmap; the metric page with a version timeline, side-by-side diff and the rule lens; the builder.
5. **Lab results**: upload with the consent dialog naming provider and model; review as a split view (PDF page with the row outlined beside the row list and editor; Enter accepts, J/K move, E edits); results by analyte linking to their trend ([lab-documents.md](lab-documents.md)).
6. **Settings**: grouped navigation (You, Sources, Access, Data, System), one card pattern (`lib/settings/Card.svelte`) for profile and time zones, sources (app credentials: set, replace, remove, read-only when set by the environment; sidecars: list, add with the secret shown once, remove), devices (pairing QR, origins), API keys, AI providers, retention, backups and export, security and system status.

## Rule builder

The builder (`src/routes/(app)/rules/new`) is a stepper with a live preview sidebar; it follows [resolution.md](resolution.md) in five steps and starts from the rule in effect, a saved version, or an empty rule:

1. **Metric**: from the catalogue (`GET /rules`, plus `GET /metrics` codes without a rule).
2. **Sources**: ordered groups of selectors (ORed lists of ANDed conditions) with values seen in the data and the active rules as suggestions and one-click chips for origins, device types and relayed or direct (`GET /origins`, `GET /source-devices`); up/down buttons to reorder; exclusions; a relay-exclusion suggestion.
3. **Strategy**: plain-language cards ("Use the first source with data", "Average the sources", "Take the highest", …) and within-group options. A sum (across or within groups) cannot be saved until the duplicate-risk checkbox adds `cross_source_sum_duplicate_risk`.
4. **Window and quality**: window (the server checks it against the metric), coverage, plausible range, flags, staleness, wear, sleep alignment, `follow` and `compose`. Extensions the form does not edit (`contexts`) are kept.
5. **Review**: the rule JSON, its diff against the rule in effect, and the last 14 days for the draft versus the active rule with a per-day diff and explanations (`POST /resolution/preview`; "preview unavailable" on 404/503).

Saving (`POST /rules/{metric}/versions`) creates a new version; server field errors return to their step and input. The metric page (`rules/[metric]`) lists versions with a field diff between any two and activates any of them. The catalogue and metric pages show the 90-day `GET /coverage` heatmap when the endpoint answers; the metric page filters it by origin app.

## Design system

Design system v3 ([E23](../plan/E23-chart-redesign/README.md)), dark first: near-black ground, cards that fade from a raised surface with a hairline border, one teal accent, numbers in tabular figures, and a hue per metric. The light theme mirrors every token. The approved canvas is the reference; the code is the source of truth.

- **Tokens** (`tokens.css`): every colour is `light-dark(light, dark)`, so `color-scheme` (system or `data-theme`) picks the theme. Groups: `--color-*` (surfaces, text, accent, link, focus, feedback, the selected `--color-pill`), `--card-bg`, `--status-*`, `--metric-*`, `--src-*`, `--stage-*`, `--chart-*`; `--text-2xs`…`--text-display`, `--space-1`…`--space-8` (4–48 px), `--radius-xs|sm|md|lg|pill`, `--control-h`, `--tile-size(-sm|-lg)`, `--shadow-1|2`, `--focus-ring`, `--glow-selected`; for controls `--color-control-border|border-hover|backdrop|scrollbar`, `--mask-check|dash`, `--icon-chevron` (per theme), `--ease`, `--duration`. Motion is minimal and off under `prefers-reduced-motion`.
- **Metric hues** (`lib/ui/metric.ts`, `MetricTile`): the catalogue section picks one of activity, energy, heart, HRV, sleep, body, blood pressure or respiratory (`--metric-<hue>`), each with a tinted icon tile (`--metric-<hue>-tint`); HRV and energy codes, derived codes and family cards refine it, anything else is neutral. Sleep stages: deep, REM, light, awake (`--stage-*`).
- **Primitives** (`lib/ui/`): `Button` (`variant`, `size`, `icon`, `loading`), `Switch`, `Segmented` (period pills: the selected one is a light pill on the dark track), `Tabs` (link tabs with an accent indicator), `Chip` (source chip, tinted by its source), `MetricTile`, `Badge` (tinted tones), `EmptyState`, `Skeleton`, `CopyValue` (a value with a Copy button), `Icon`/`icons.ts`, `Logo`; `Modal` (header, scrolling body, optional `footer`; also a side `drawer`), `TextField` (hint, error, `prefix`/`suffix`, `multiline`), `ProblemAlert`, `ExplainPopover`, `StatusIcon` and `HealthBadge` in `lib/components/`. Add a primitive when a second page needs it.
- **Controls** (`base.css`): native elements are styled globally, so pages use them bare and never style their own: text inputs, `<select>` (custom chevron), `<textarea>`, checkbox and radio (drawn by CSS), `input[role=switch]`. Shared classes: `.btn` with `.primary`, `.ghost`, `.danger`, `.link`, `.sm`, `.lg`, `.icon-btn`; `.field`, `.input-group`/`.affix`; `label.check`, `.option-card`; `.segmented`, `.tabs`, `.chip`, `.popover`, `.inline-alert` (`.ok`, `.info`, `.warn`, `.error`), `.card`. One focus ring (outline plus `--focus-ring` glow), hover, pressed and disabled states, 44 px controls on a coarse pointer, no colour transitions.
- **Data status** (`lib/ui/status.ts`, `ResultStatus`): a shape, a colour and a word for each of direct (circle), fallback (diamond), calculated (triangle), overridden (square), partial (half circle) and no data (ring). Status colours describe data state only.
- **Source colours** (`lib/ui/source.ts`): a provider maps to a `.src-*` class that sets `--src`; Apple Health, WHOOP, Withings, Garmin and push/manual are fixed, others take one of three extra colours by a stable hash. Series also differ by dash, so colour is never the only cue.
- **Stat tiles**: a metric's tile, value, unit and a neutral sub-line; the selected tile (the one driving the chart below) takes a metric-hue border and a soft glow.
- **Copy**: neutral words for values ("30-day mean", "vs 90-day mean"), never good or bad, no advice; plain language for errors; lab values, labels, units and ranges "as printed".

## Chart grammar

`chartFor(metric)` in `lib/charts/grammar.ts` picks the view from the catalogue (`GET /metrics`), so no metric has its own chart code:

| Catalogue | View | Kit |
| --- | --- | --- |
| `intensive` | line with min–max band | `TimeSeries` + `band` |
| `additive` | bars per window | `Bars` |
| `latest` | step line with readings | `TimeSeries step` |
| `daily_summary` | line with baseline | `TimeSeries` + `baseline` |
| `sleep_derived` | stage stack per night, hypnogram, bedtime and wake range bars | `Bars` (stacks by stage), `Hypnogram`, `RangeBars` |
| group `bp_*` | dumbbells | `RangeDumbbell` |
| events | timeline lanes | `EventLanes` |
| lab analytes | points with the printed range | `TimeSeries` (`style: 'dots'`) + `band` |

Any x/y view also takes source overlays (one series per source, from `GET /sources/series`, told apart by colour and dash), and a metric page adds a `Histogram` of its values in the range and a `BrushNavigator` (`bind:view` shared with the chart). Marks take the metric hue (`--metric`); overlays take source colours; gaps are shaded, never zero. Also in the kit: `Sparkline`, `StageStack` (one night's stages as one bar), `CoverageStrip`, `RangePicker`, `ChartTooltip`, `ChartTable`; `Sparkline`, `StageStack` and `CoverageStrip` are plain SVG or HTML. Every x/y chart draws in `ChartFrame` (LayerChart `ChartCore` and `Svg`): one tab stop with arrow keys, Home/End and PageUp/PageDown between points (announced politely), Enter to open a point, drag to zoom, a "Show as a table" fallback, and the `vx-chart-render` User Timing measure. A draft series is `style: 'ghost'`; non-direct points carry their status marker.

### Interaction model

Hovering, touching or arrowing to a point moves a crosshair and a tooltip with the date, value and unit, status (shape and word) and source chips (`Series.providers`, from `GET /resolved/series`). A click or tap pins the card, which then offers the chart's `actions` (on a metric page: Explain, Override, Raw records); Enter runs `onselect`. A period switch (7D/30D/90D/1Y) animates marks to their new positions, with no motion under reduced motion. On the dashboard the selected stat tile drives the hero chart. A metric page adds a brush navigator under the chart (drag the window, wheel zooms) and toggles for source overlays. Touch scrubs through points. The keyboard model and the "Show as a table" fallback stay as above.
