# Frontend

## Technology

- SvelteKit with `adapter-static`, SSR off, TypeScript. Built in CI and embedded in `vitamux` with `go:embed`; Node is needed only at build time.
- Generated OpenAPI client; `uPlot` for series (a 14,400-point day must render quickly); `pdf.js` lazy-loaded for lab review. No component framework.
- Budget: ≤ 300 KiB gzip initial JS, excluding the lazy chunks. Strict CSP (no inline scripts).
- Accessibility: keyboard navigation, labelled controls, status shown by shape and colour (never colour alone). Responsive, but not a mobile app.

Alternatives rejected: htmx (the rule builder, charts, and PDF review need real JS anyway) and React (heavier, more churn).

## Code layout

- Imports use `#lib/…` (SvelteKit 3 subpath imports) with explicit `.ts` extensions; relative imports inside `src/lib`.
- Sections are routes under `src/routes/(app)/` (listed in `src/lib/nav.ts`), whose layout is the shell and the session guard; `/login` sits outside it. A page sets `<title>X · Vitamux</title>` and one `<h1>`.
- API calls go through `api` in `src/lib/api/client.ts` (`openapi-fetch` over the generated `schema.d.ts`). It adds `X-CSRF-Token` on mutations, sends any other 401 to `/login?next=…&reason=expired`, and turns every failure into a `Problem`. Show it with `ProblemAlert`; map field errors to inputs with `fieldErrors` and `TextField`.
- Styling uses the custom properties in `src/lib/styles/tokens.css` and the few shared classes in `base.css`. Status uses `StatusIcon` (shape plus colour). No inline `style` attributes, which the CSP blocks.
- E2E: `npm run test:e2e` builds and runs Playwright (`web/e2e`) against `vite preview`. The API is stubbed with `page.route` (`e2e/fake-api.ts`), and CSP violations fail the test.

## Navigation

1. **Today**: key resolved metrics with source chips; connection health; alerts (reauth, drift, stale backup).
2. **Connections**: list, then detail tabs Overview, Streams, History, Backfill, Settings. A single auth wizard handles OAuth redirect or credentials → MFA. Manual sync. Unofficial badge.
3. **Data**:
   - daily view with status icons (direct, fallback, calculated, overridden) and explanation popovers;
   - **All sources** drilldown: chart overlay, included/excluded table, provenance links, override actions;
   - sleep hypnogram comparison; workout clusters.
4. **Rules**: metric catalogue with a 90-day per-source coverage heatmap; guided builder; version history, diff, activate.
5. **Lab results**: upload, consent dialog, review (PDF + rows), confirmed results by analyte ([lab-documents.md](lab-documents.md)).
6. **Settings**: profile and timezone periods, devices (Apple pairing, origins), API keys, AI providers, retention, backups and export, security (password, TOTP, sessions), system status.

## Rule builder

The builder follows [resolution.md](resolution.md) in five steps:

1. **Window**: only the windows allowed for the metric.
2. **Sources**: groups built from chips of providers, devices, and origins actually seen; drag to order; exclusions; a relay-exclusion suggestion.
3. **Operation**: plain-language cards ("Use the first source with data", "Average the sources", "Take the highest", …). Duplicate-risk operations require a checkbox acknowledgement.
4. **Quality**: coverage, plausible range, manual entries, staleness, sleep alignment.
5. **Preview**: the last 14 days for the draft versus the active rule, with a per-day diff and explanations (`POST /resolution/preview`).

Saving creates a new version.
