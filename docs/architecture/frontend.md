# Frontend

## Technology

- SvelteKit with `adapter-static`, SSR off, TypeScript. Built in CI and embedded in `vitamux` with `go:embed`; Node is needed only at build time.
- Generated OpenAPI client; `uPlot` for series (a 14,400-point day must render quickly); `pdf.js` lazy-loaded for lab review (dynamic import in `lib/lab/PdfViewer.svelte`, no WebAssembly; the row outline is an SVG over the canvas). No component framework.
- Budget: ≤ 300 KiB gzip initial JS, excluding the lazy chunks. Strict CSP (no inline scripts).
- Accessibility: keyboard navigation, labelled controls, status shown by shape and colour (never colour alone). Responsive, but not a mobile app.

Alternatives rejected: htmx (the rule builder, charts, and PDF review need real JS anyway) and React (heavier, more churn).

## Code layout

- Imports use `#lib/…` (SvelteKit 3 subpath imports) with explicit `.ts` extensions; relative imports inside `src/lib`.
- Sections are routes under `src/routes/(app)/` (listed in `src/lib/nav.ts`), whose layout is the shell and the session guard; `/login` sits outside it. A page sets `<title>X · Vitamux</title>` and one `<h1>`.
- API calls go through `api` in `src/lib/api/client.ts` (`openapi-fetch` over the generated `schema.d.ts`). It adds `X-CSRF-Token` on mutations, sends any other 401 to `/login?next=…&reason=expired`, and turns every failure into a `Problem`. Show it with `ProblemAlert`; map field errors to inputs with `fieldErrors` and `TextField`.
- Styling uses the custom properties in `src/lib/styles/tokens.css` and the few shared classes in `base.css`. Status uses `StatusIcon` (shape plus colour). No inline `style` attributes, which the CSP blocks.
- E2E: `npm run test:e2e` builds and runs Playwright (`web/e2e`) against `vite preview`. The API is stubbed with `page.route` (`e2e/fake-api.ts`), and CSP violations fail the test. `e2e/a11y.spec.ts` runs axe over every section page (serious or critical fails). `make test-e2e-stack` runs one smoke against a real server on a throwaway database (`scripts/e2e-stack.sh`, `web/e2e-stack`).
- Gates in CI (`web` job): `npm run check` (svelte-check, warnings fail), eslint, `npm run budget` (`scripts/bundle-budget.mjs`, per-route initial JS), Playwright.

## Navigation

Planned ([E21](../plan/E21-visualisation/README.md), v0.4.0): a redesign of every section on design system v2, Dashboard and Explore in place of Today and Data, and a rule lens on every metric chart.

1. **Today**: key resolved metrics with source chips; connection health; alerts (reauth, drift, stale backup).
2. **Connections**: list, then detail tabs (`?tab=`) Overview, Streams, Backfills, History, Settings. A single auth wizard handles the OAuth redirect (credentials → MFA once `AuthStep` carries a prompt); the callback's `?connected=`/`?auth_error=` show on the list. Manual sync, backfills with retry and cancel, pause, delete with keep/delete data. Unofficial badge from `Connection.official`. Planned ([E20](../plan/E20-guided-setup/README.md)): **Connect a source** shows each provider's setup state, with the Withings app wizard, the sidecar-not-running card, and one sign-in and MFA dialog for Garmin and WHOOP.
3. **Data**:
   - daily view with status icons (direct, fallback, calculated, overridden) and explanation popovers;
   - **All sources** drilldown: chart overlay, included/excluded table, provenance links, override actions;
   - sleep hypnogram comparison; workout clusters.
4. **Rules**: metric catalogue with a 90-day per-source coverage heatmap; guided builder; version history, diff, activate.
5. **Lab results**: upload, consent dialog naming provider and model, review (`lab/documents/[id]`: PDF page with the row outlined beside the row editor), confirmed results by analyte with history, delete keeping or deleting results ([lab-documents.md](lab-documents.md)).
6. **Settings**: profile and timezone periods, devices (Apple pairing QR with countdown, device list with requested types, possibly-denied hints, resync and revoke; origin classification as native, relayed or direct; E15), API keys, AI providers, retention, backups and export, security (password, TOTP, sessions), system status.

## Rule builder

The builder (`src/routes/(app)/rules/new`) follows [resolution.md](resolution.md) in five steps and starts from the rule in effect, a saved version, or an empty rule:

1. **Metric**: from the catalogue (`GET /rules`, plus `GET /metrics` codes without a rule).
2. **Sources**: ordered groups of selectors (ORed lists of ANDed conditions) with values seen in the data and the active rules as suggestions and one-click chips for origins, device types and relayed or direct (`GET /origins`, `GET /source-devices`); up/down buttons to reorder; exclusions; a relay-exclusion suggestion.
3. **Strategy**: plain-language cards ("Use the first source with data", "Average the sources", "Take the highest", …) and within-group options. A sum (across or within groups) cannot be saved until the duplicate-risk checkbox adds `cross_source_sum_duplicate_risk`.
4. **Window and quality**: window (the server checks it against the metric), coverage, plausible range, flags, staleness, wear, sleep alignment, `follow` and `compose`. Extensions the form does not edit (`contexts`) are kept.
5. **Review**: the rule JSON, its diff against the rule in effect, and the last 14 days for the draft versus the active rule with a per-day diff and explanations (`POST /resolution/preview`; "preview unavailable" on 404/503).

Saving (`POST /rules/{metric}/versions`) creates a new version; server field errors return to their step and input. The metric page (`rules/[metric]`) lists versions with a field diff between any two and activates any of them. The catalogue and metric pages show the 90-day `GET /coverage` heatmap when the endpoint answers; the metric page filters it by origin app.
