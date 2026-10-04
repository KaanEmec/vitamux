// The rule lens's endpoints (J21.10) on top of rules-fake.ts and devices-fake.ts: GET /metrics/{code}
// with the windows and strategies a rule may use (internal/api/metrics.go), and a 422 with a field
// pointer when min_coverage is above 0.9, to test that server field errors reach their control.
// Values are synthetic.
import { mergeTests, type Route } from '@playwright/test';
import { test as devicesTest, expect } from './devices-fake';
import { test as rulesTest } from './rules-fake';

const pooled = ['single_source', 'first_available', 'mean_across_sources', 'minimum_across_sources', 'maximum_across_sources', 'latest', 'earliest'];
const metrics: Record<string, Record<string, unknown>> = {
	heart_rate: { aggregation: 'intensive', unit: 'bpm', windows: ['bucket', 'hour', 'local_day'], strategies: pooled },
	steps: { aggregation: 'additive', unit: 'count', windows: ['hour', 'local_day'], strategies: [...pooled, 'sum_across_sources'] }
};

const json = (r: Route, status: number, body: unknown) =>
	r.fulfill({ status, contentType: status < 300 ? 'application/json' : 'application/problem+json', body: JSON.stringify(body) });

export const test = mergeTests(rulesTest, devicesTest).extend<{ lens: void }>({
	lens: [
		async ({ page, rules }, use) => {
			await page.route('**/api/v1/metrics/*', (r) => {
				const code = new URL(r.request().url()).pathname.split('/').pop() ?? '';
				const m = metrics[code];
				if (!m) return json(r, 404, { type: 'about:blank', title: 'not_found', status: 404, code: 'not_found', detail: 'no such metric' });
				return json(r, 200, { code, section: 'synthetic', kinds: ['sample'], plausible_range: [], provider_scoped: false, selection_only: false, ...m });
			});
			await page.route('**/api/v1/rules/*/versions', (r) => {
				const body = r.request().method() === 'POST' ? (r.request().postDataJSON() as { spec: { quality?: { min_coverage?: number } } }) : null;
				if (!body || !((body.spec.quality?.min_coverage ?? 0) > 0.9)) return r.fallback();
				rules.posted.push(body.spec);
				return json(r, 422, {
					type: 'about:blank', title: 'validation_failed', status: 422, code: 'validation_failed', detail: 'invalid rule', request_id: 'req-e2e',
					errors: [{ pointer: '/spec/quality/min_coverage', detail: 'must be at most 0.9' }]
				});
			});
			await use();
		},
		{ auto: true }
	]
});

export { expect };
