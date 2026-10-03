// A stateful stand-in for the rules, coverage and preview endpoints used by the Rules section
// (J11.4), on top of fake-api.ts. It mirrors internal/api/rules.go: the first save of a metric
// with a built-in copies it as version 1, a sum without its acknowledgement is 409
// rule_warning_unacknowledged, and bad group ids are 422 with field pointers. Values are synthetic.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
interface Version {
	ref: string;
	metric: string;
	version: number;
	builtin: boolean;
	active: boolean;
	spec: Json;
	based_on: string | null;
	note: string | null;
	created_by: string | null;
	created_at: string | null;
	reason?: string;
}

const builtins: { spec: Json; reason: string }[] = [
	{
		reason: 'Chest straps are ECG-class, then wrist devices by independent validation.',
		spec: {
			schema: 'vitamux.rule/1',
			metric: 'heart_rate',
			window: { kind: 'bucket', size: '5m' },
			groups: [
				{ id: 'chest_strap', match: [{ device_type: 'chest_strap' }] },
				{ id: 'apple_watch', match: [{ provider: 'apple_health', device_type: 'watch' }] },
				{ id: 'garmin', match: [{ provider: 'garmin' }] }
			],
			strategy: { op: 'first_available' },
			quality: { plausible_range: [25, 230], exclude_flags: ['manual_entry'] }
		}
	},
	{
		reason: 'Selection only (definitions differ); ranked by nightly error against a chest strap.',
		spec: {
			schema: 'vitamux.rule/1',
			metric: 'resting_heart_rate',
			window: { kind: 'local_day' },
			groups: [
				{ id: 'oura', match: [{ provider: 'oura' }] },
				{ id: 'garmin', match: [{ provider: 'garmin' }] }
			],
			strategy: { op: 'first_available' },
			quality: { max_staleness: '36h' }
		}
	},
	{
		reason: 'Watch, ring, band, phone; hours resolve separately.',
		spec: {
			schema: 'vitamux.rule/1',
			metric: 'steps',
			window: { kind: 'local_day' },
			groups: [
				{ id: 'watch', match: [{ device_type: 'watch' }] },
				{ id: 'phone', match: [{ device_type: 'phone' }] }
			],
			strategy: { op: 'first_available' },
			compose: { from: 'hour', op: 'first_available' }
		}
	}
];

const builtinVersion = (b: (typeof builtins)[number]): Version => ({
	ref: `builtin:${b.spec.metric}:1`,
	metric: b.spec.metric as string,
	version: 1,
	builtin: true,
	active: true,
	spec: b.spec,
	based_on: null,
	note: null,
	created_by: null,
	created_at: null,
	reason: b.reason
});

export class RulesApi {
	/** Owner versions per metric, oldest first. */
	owned = new Map<string, Version[]>();
	/** Specs received by POST /rules/{metric}/versions, accepted or not. */
	posted: Json[] = [];
	/** Bodies received by POST /resolution/preview. */
	previews: Json[] = [];
	/** 'ok' answers the preview with per-day results; a number answers that status. */
	preview: 'ok' | number = 404;
	/** 'ok' answers GET /coverage with a matrix; a number answers that status. */
	coverage: 'ok' | number = 404;

	constructor(private page: Page) {}

	async install() {
		const r = (glob: string | RegExp, h: (route: Route, m: string[]) => unknown) =>
			this.page.route(glob, (route) => {
				const path = new URL(route.request().url()).pathname;
				return h(route, path.split('/').slice(4));
			});
		await r('**/api/v1/rules', (route) => json(route, 200, { rules: this.activeSet() }));
		await r('**/api/v1/metrics', (route) =>
			json(route, 200, { metrics: [...builtins.map((b) => ({ code: b.spec.metric })), { code: 'skin_temperature' }] })
		);
		await r('**/api/v1/rules/*/versions', (route, [metric]) =>
			route.request().method() === 'POST' ? this.create(route, metric) : this.versions(route, metric)
		);
		await r('**/api/v1/rules/*/activate', (route, [metric]) => this.activate(route, metric));
		await r(/\/api\/v1\/coverage(\?|$)/, (route) => this.coverageMatrix(route));
		await r('**/api/v1/resolution/preview', (route) => this.resolvePreview(route));
	}

	/** Seeds an owner rule: the built-in copy as version 1 and `spec` as active version 2. */
	seedOwned(metric: string, spec: Json) {
		const b = builtins.find((x) => x.spec.metric === metric)!;
		this.owned.set(metric, [
			this.version(metric, 1, b.spec, `builtin:${metric}:1`, null),
			{ ...this.version(metric, 2, spec, null, 'tuned'), active: true }
		]);
	}

	private version(metric: string, n: number, spec: Json, basedOn: string | null, note: string | null): Version {
		return {
			ref: `rule:${metric}:${n}`,
			metric,
			version: n,
			builtin: false,
			active: false,
			spec,
			based_on: basedOn,
			note,
			created_by: 'session:owner',
			created_at: `2026-10-0${n}T09:00:00Z`
		};
	}

	private activeSet(): Version[] {
		return builtins.map((b) => this.owned.get(b.spec.metric as string)?.find((v) => v.active) ?? builtinVersion(b));
	}

	private versions(route: Route, metric: string) {
		const own = this.owned.get(metric);
		if (own?.length) return json(route, 200, { versions: [...own].reverse() });
		const b = builtins.find((x) => x.spec.metric === metric);
		if (!b) return problem(route, 404, 'not_found', 'no such metric');
		return json(route, 200, { versions: [builtinVersion(b)] });
	}

	private create(route: Route, metric: string) {
		const body = route.request().postDataJSON() as { spec: Json; note?: string; activate?: boolean };
		const spec = body.spec;
		this.posted.push(spec);
		if (spec.metric !== metric) {
			return problem(route, 422, 'validation_failed', 'invalid rule', [{ pointer: '/spec/metric', detail: 'must be the metric in the path' }]);
		}
		const sums =
			(spec.strategy as Json)?.op === 'sum_across_sources' || (spec.within_source as Json | undefined)?.intra_group === 'sum';
		if (sums && !((spec.acknowledged_warnings as string[] | undefined) ?? []).includes('cross_source_sum_duplicate_risk')) {
			return problem(route, 409, 'rule_warning_unacknowledged', 'the rule sums across sources: acknowledge cross_source_sum_duplicate_risk in acknowledged_warnings', [
				{ pointer: '/spec/acknowledged_warnings', detail: 'sum_across_sources must be acknowledged' }
			]);
		}
		const bad = (spec.groups as { id: string }[]).flatMap((g, i) =>
			/^[a-z][a-z0-9_]{0,31}$/.test(g.id) ? [] : [{ pointer: `/spec/groups/${i}/id`, detail: 'must start with a lowercase letter and use only a-z, 0-9 and _' }]
		);
		if (bad.length) return problem(route, 422, 'validation_failed', 'invalid rule', bad);

		const own = this.owned.get(metric) ?? [];
		const b = builtins.find((x) => x.spec.metric === metric);
		if (!own.length && b) own.push(this.version(metric, 1, b.spec, `builtin:${metric}:1`, null));
		const v = this.version(metric, own.length + 1, spec, null, body.note ?? null);
		own.push(v);
		this.owned.set(metric, own);
		if (body.activate) for (const x of own) x.active = x === v;
		return json(route, 201, v);
	}

	private activate(route: Route, metric: string) {
		const { version } = route.request().postDataJSON() as { version: number };
		const own = this.owned.get(metric) ?? [];
		const v = own.find((x) => x.version === version);
		if (!v) return problem(route, 404, 'not_found', 'no such rule version');
		for (const x of own) x.active = x === v;
		return json(route, 200, v);
	}

	private coverageMatrix(route: Route) {
		if (this.coverage !== 'ok') return problem(route, this.coverage, 'not_found', 'not implemented yet');
		const q = new URL(route.request().url()).searchParams;
		const days = (n: number) => Array.from({ length: 90 }, (_, i) => (i % 3 < n ? 0.4 + (i % 5) / 10 : 0));
		const rows = [
			{ metric: 'heart_rate', source: 'garmin', days: days(2) },
			{ metric: 'heart_rate', source: 'apple_watch', days: days(1) },
			{ metric: 'steps', source: 'apple_watch', days: days(3) }
		].filter((r) => !q.getAll('metric').length || q.getAll('metric').includes(r.metric));
		return json(route, 200, { start_date: q.get('start_date'), end_date: q.get('end_date'), rows });
	}

	private resolvePreview(route: Route) {
		const body = route.request().postDataJSON() as Json;
		this.previews.push(body);
		if (this.preview !== 'ok') return problem(route, this.preview, 'not_found', 'not implemented yet');
		const start = Date.parse(`${body.start_date}T00:00:00Z`);
		const changed = new Set([2, 5, 9]);
		const value = (draft: boolean, i: number) => ({
			status: draft && changed.has(i) ? 'calculated' : 'direct',
			value: draft && changed.has(i) ? 61.5 : 60,
			unit: 'bpm',
			inputs: [{ group: draft && changed.has(i) ? 'garmin' : 'chest_strap', status: 'used', selected: true }],
			explanation: draft && changed.has(i) ? 'Mean of chest_strap and garmin: 61.5 bpm.' : 'chest_strap: 60 bpm.'
		});
		const days = Array.from({ length: 14 }, (_, i) => ({
			local_date: new Date(start + i * 86_400_000).toISOString().slice(0, 10),
			active: value(false, i),
			draft: value(true, i)
		}));
		return json(route, 200, { days });
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

function problem(r: Route, status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
	return r.fulfill({
		status,
		contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e', errors })
	});
}

export const test = base.extend<{ rules: RulesApi }>({
	rules: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const rules = new RulesApi(page);
			await rules.install();
			await use(rules);
		},
		{ auto: true }
	]
});

export { expect };
