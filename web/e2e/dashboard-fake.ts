// A stateful stand-in for the Dashboard endpoints (J21.7), on top of fake-api.ts: the stored
// layout (GET/PUT /settings/dashboard), the catalogue, GET /resolved/summary and the alert
// sources (connections, jobs, system status). All values are synthetic and deterministic.
//
// Seed: the curated default layout; sleep, resting heart rate, HRV (RMSSD), steps, VO2 max,
// weight, blood pressure, SpO2 (a fallback), respiratory rate and active energy have data;
// HRV (SDNN) has none, so its card stays hidden. Steps of today are partial. A Withings
// connection needs reauthorization and the last backup is ten days old (two alerts).
// `empty` removes all data and connections (a fresh install).
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
export interface Card {
	metric: string;
	size: 'S' | 'M' | 'L';
	hidden: boolean;
}

export const defaultLayout: Card[] = (
	[
		['sleep', 'L'],
		['resting_heart_rate', 'M'],
		['hrv_rmssd_nightly', 'M'],
		['hrv_sdnn', 'M'],
		['steps', 'M'],
		['vo2max', 'S'],
		['weight', 'S'],
		['blood_pressure', 'S'],
		['spo2', 'S'],
		['respiratory_rate', 'S'],
		['active_energy', 'S']
	] as [string, Card['size']][]
).map(([metric, size]) => ({ metric, size, hidden: false }));

const pad = (n: number) => String(n).padStart(2, '0');
const iso = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
/** The browser's local date, which the page treats as today. */
export const today = () => iso(new Date());
export const daysAgo = (n: number) => {
	const d = new Date();
	d.setDate(d.getDate() - n);
	return iso(d);
};

interface Spec {
	unit: string;
	base: number;
	amp: number;
	agg: string;
	group: string;
	provider: string;
	status?: string;
	section: string;
}
const specs: Record<string, Spec> = {
	resting_heart_rate: { unit: 'bpm', base: 52, amp: 4, agg: 'daily_summary', group: 'whoop', provider: 'whoop', section: 'Heart and circulation' },
	hrv_rmssd_nightly: { unit: 'ms', base: 64, amp: 10, agg: 'daily_summary', group: 'whoop', provider: 'whoop', section: 'Heart and circulation' },
	steps: { unit: 'count', base: 8700, amp: 3000, agg: 'additive', group: 'apple_watch', provider: 'apple_health', section: 'Activity' },
	vo2max: { unit: 'mL/kg/min', base: 48, amp: 1, agg: 'latest', group: 'apple_watch', provider: 'apple_health', section: 'Heart and circulation' },
	weight: { unit: 'kg', base: 74.5, amp: 0.8, agg: 'latest', group: 'scale', provider: 'withings', section: 'Body composition' },
	spo2: { unit: '%', base: 96, amp: 1, agg: 'intensive', group: 'whoop', provider: 'whoop', status: 'fallback', section: 'Respiration and oxygen' },
	respiratory_rate: { unit: 'breaths/min', base: 14, amp: 1, agg: 'intensive', group: 'apple_watch', provider: 'apple_health', section: 'Respiration and oxygen' },
	active_energy: { unit: 'kcal', base: 700, amp: 150, agg: 'additive', group: 'apple_watch', provider: 'apple_health', section: 'Activity' }
};

// A catalogue slice: the codes above plus parts of the two families and one more body metric.
const catalogue = [
	...Object.entries(specs).map(([code, s]) => ({ code, section: s.section, unit: s.unit, aggregation: s.agg })),
	{ code: 'body_fat_ratio', section: 'Body composition', unit: '%', aggregation: 'latest', group: 'body_composition' },
	{ code: 'bp_systolic', section: 'Blood pressure', unit: 'mmHg', aggregation: 'latest', group: 'bp_reading' },
	{ code: 'bp_diastolic', section: 'Blood pressure', unit: 'mmHg', aggregation: 'latest', group: 'bp_reading' },
	{ code: 'sleep_total', section: 'Sleep', unit: 's', aggregation: 'sleep_derived' },
	{ code: 'sleep_deep', section: 'Sleep', unit: 's', aggregation: 'sleep_derived' }
].map((m) => ({ kinds: [], windows: ['local_day'], strategies: ['first_available'], plausible_range: [0, 1000], provider_scoped: false, selection_only: false, ...m }));

const providers = [
	{ code: 'whoop', name: 'WHOOP', official: false, auth_kind: 'interactive_mfa', remote: true, available: true },
	{ code: 'apple_health', name: 'Apple Health', official: true, auth_kind: null, remote: false, available: true },
	{ code: 'withings', name: 'Withings', official: true, auth_kind: 'oauth2', remote: false, available: true }
];

const hour = 3_600_000;
const t0 = '2026-09-01T08:00:00Z';
const connection = (id: string, provider: string, over: Json = {}) => ({
	id, provider, mode: 'in_process', status: 'active', official: true, upstream: null, health: 'ok', health_reason: null,
	last_success_at: new Date(Date.now() - hour).toISOString(), last_error_class: null, consecutive_failures: 0, created_at: t0, updated_at: t0, ...over
});

export class DashboardApi {
	/** The saved layout; null until a PUT, so GET answers the default. */
	stored: Card[] | null = null;
	/** Bodies of PUT /settings/dashboard, in order. */
	puts: { version: number; cards: Card[] }[] = [];
	/** A fresh install: no data, no connections. */
	empty = false;
	/** Status of GET /settings/dashboard; 503 simulates the endpoint not being ready. */
	layoutStatus = 200;
	/** Dates of GET /resolved/summary requests (undefined: the owner's today). */
	summaryDates: (string | null)[] = [];

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const method = r.request().method();
		if (path === '/settings/dashboard' && method === 'GET') {
			if (this.layoutStatus !== 200) return problem(r, this.layoutStatus, 'unavailable', 'not ready');
			return json(r, 200, { version: 1, cards: this.stored ?? defaultLayout, is_default: this.stored === null });
		}
		if (path === '/settings/dashboard' && method === 'PUT') {
			const body = r.request().postDataJSON() as { version: number; cards: Card[] };
			this.puts.push(body);
			this.stored = body.cards;
			return json(r, 200, { version: 1, cards: body.cards });
		}
		if (path === '/metrics') return json(r, 200, { metrics: catalogue });
		if (path === '/resolved/summary') return this.summary(r, url.searchParams);
		if (path === '/providers') return json(r, 200, { providers });
		if (path === '/connections') {
			const list = this.empty ? [] : [connection('conn_' + 'a'.repeat(32), 'apple_health', { mode: 'push', official: null }), connection('conn_' + 'b'.repeat(32), 'withings', { status: 'needs_reauth', health: 'needs_reauth', consecutive_failures: 1 })];
			return json(r, 200, { connections: list });
		}
		if (path === '/jobs') return json(r, 200, { jobs: [], has_more: false });
		if (path === '/system/status') return json(r, 200, { last_backup_at: this.empty ? null : new Date(Date.now() - 10 * 24 * hour).toISOString() });
		return r.fallback();
	}

	private summary(r: Route, q: URLSearchParams) {
		const date = q.get('date');
		this.summaryDates.push(date);
		const on = date ?? today();
		const metrics = q.getAll('metrics').flatMap((m) => m.split(',')).filter(Boolean);
		return json(r, 200, { date: on, timezone: 'Europe/Amsterdam', metrics: Object.fromEntries(metrics.map((m) => [m, this.metric(m, on)])) });
	}

	private metric(code: string, date: string): Json {
		const dates = Array.from({ length: 30 }, (_, i) => addDays(date, i - 29));
		const empty = this.empty || code === 'hrv_sdnn' || !(code in specs || code === 'sleep' || code === 'blood_pressure');
		const series = dates.map((d) => point(code, d));
		const stats = [7, 30, 90].map((days) => rollup(code, series, days, date, empty));
		const spec = specs[code];
		const value: Json = empty
			? { status: 'no_data', explanation: 'No source had a value.' }
			: {
					status: spec?.status ?? 'direct',
					value: series[29],
					...(spec ? { unit: spec.unit } : {}),
					...(code === 'steps' && date === today() ? { partial: true } : {}),
					inputs: [{ group: spec?.group ?? (code === 'sleep' ? 'whoop' : 'bp_monitor'), status: 'used', selected: true, sources: [{ provider: spec?.provider ?? (code === 'sleep' ? 'whoop' : 'withings') }] }],
					explanation: 'Synthetic value.'
				};
		return {
			metric: code,
			...(spec ? { unit: spec.unit } : {}),
			value,
			sparkline: dates.map((d, i) => (empty ? { local_date: d, status: 'no_data' } : { local_date: d, status: 'direct', value: series[i] })),
			stats
		};
	}
}

// Deterministic values: a slow wave plus a small date-keyed wobble.
function point(code: string, date: string): unknown {
	const n = Number(date.slice(5, 7)) * 31 + Number(date.slice(8, 10));
	const wobble = ((n * 37) % 11) / 10 - 0.5; // -0.5 .. 0.5
	if (code === 'sleep') {
		const total = 26_000 + wobble * 3000;
		return { sleep_total: total, sleep_in_bed: total + 2400, sleep_deep: 4700, sleep_light: total - 4700 - 8200 - 1400, sleep_rem: 8200, sleep_awake: 1400 };
	}
	if (code === 'blood_pressure') return { bp_systolic: 118 + Math.round(wobble * 8), bp_diastolic: 76 + Math.round(wobble * 4), bp_pulse: 58 };
	const s = specs[code];
	if (!s) return null;
	const v = s.base + wobble * s.amp * 2;
	return s.agg === 'additive' ? Math.round(v) : Math.round(v * 10) / 10;
}

const lead = (code: string) => (code === 'sleep' ? 'sleep_total' : 'bp_systolic');
const scalar = (code: string, v: unknown) => (typeof v === 'number' ? v : ((v as Record<string, number>)[lead(code)] ?? 0));
const values = (code: string, series: unknown[]) => series.map((v) => scalar(code, v));
const stat = (xs: number[]) => ({ n: xs.length, mean: xs.reduce((a, b) => a + b, 0) / xs.length, min: Math.min(...xs), max: Math.max(...xs) });

function rollup(code: string, series: unknown[], days: number, end: string, empty: boolean) {
	const tail = series.slice(-Math.min(days, 30));
	const base = { start_date: addDays(end, 1 - days), end_date: end, days, n: empty ? 0 : tail.length, coverage: empty ? 0 : tail.length / days };
	if (empty) return base;
	if (code === 'sleep') return { ...base, components: { sleep_total: stat(values(code, tail)) } };
	if (code === 'blood_pressure') {
		const pick = (k: string) => stat(tail.map((v) => (v as Record<string, number>)[k]));
		return { ...base, components: { bp_systolic: pick('bp_systolic'), bp_diastolic: pick('bp_diastolic') } };
	}
	const { mean, min, max } = stat(values(code, tail));
	return { ...base, mean, min, max };
}

function addDays(date: string, n: number): string {
	const [y, m, d] = date.split('-').map(Number);
	const t = new Date(Date.UTC(y, m - 1, d + n));
	return `${t.getUTCFullYear()}-${pad(t.getUTCMonth() + 1)}-${pad(t.getUTCDate())}`;
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string) {
	return r.fulfill({
		status, contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e' })
	});
}

/** `test` with the base FakeApi (signed in) plus a fresh DashboardApi per test. */
export const test = base.extend<{ dash: DashboardApi }>({
	dash: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const dash = new DashboardApi(page);
			await dash.install();
			await use(dash);
		},
		{ auto: true }
	]
});

export { expect };
