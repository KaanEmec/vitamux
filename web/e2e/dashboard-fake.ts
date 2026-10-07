// A stateful stand-in for the Dashboard endpoints (J21.7, J23.6), on top of fake-api.ts: the stored
// layout and hero tiles (GET/PUT /settings/dashboard), the catalogue, GET /resolved/summary (with
// comparisons), the hero chart's GET /resolved/series and /resolved/trend, last night
// (GET /resolved/sleep, GET /sleep) and the alert sources (connections, jobs, system status).
// All values are synthetic and deterministic.
//
// Seed: the curated default layout; sleep, resting heart rate, HRV (RMSSD), steps, VO2 max,
// weight, blood pressure, SpO2 (a fallback), respiratory rate, active and total energy have data;
// HRV (SDNN) has none, so its card stays hidden. Steps of today are partial. A Withings
// connection needs reauthorization and the last backup is ten days old (two alerts). The
// dismissed alert keys are stored with the layout; `reauthSince` dates the reauthorization
// problem (the connection's last success), so changing it is a new occurrence.
// `empty` removes all data and connections (a fresh install).
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
export interface Card {
	metric: string;
	size: 'S' | 'M' | 'L';
	hidden: boolean;
}

const defaultHero = ['steps', 'resting_heart_rate', 'hrv_rmssd_nightly', 'weight'];

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
		['active_energy', 'S'],
		['total_energy', 'S']
	] as [string, Card['size']][]
).map(([metric, size]) => ({ metric, size, hidden: false }));

const pad = (n: number) => String(n).padStart(2, '0');
const iso = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
/** The browser's local date, which the page treats as today. */
const today = () => iso(new Date());
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
	active_energy: { unit: 'kcal', base: 700, amp: 150, agg: 'additive', group: 'apple_watch', provider: 'apple_health', section: 'Activity' },
	total_energy: { unit: 'kcal', base: 2400, amp: 300, agg: 'additive', group: 'whoop', provider: 'whoop', section: 'Activity' }
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

const setup = { setup_state: 'ready', callback_url: null, problems: [], app_credentials: null, sidecar: null, connections: 0 };
const providers = [
	{ ...setup, code: 'whoop', name: 'WHOOP', official: false, auth_kind: 'interactive_mfa', remote: true, available: true },
	{ ...setup, code: 'apple_health', name: 'Apple Health', official: true, auth_kind: 'device_pairing', remote: false, available: true },
	{ ...setup, code: 'withings', name: 'Withings', official: true, auth_kind: 'oauth2', remote: false, available: true }
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
	/** The saved hero tiles; null keeps the default. */
	storedHero: string[] | null = null;
	/** Bodies of PUT /settings/dashboard, in order. */
	puts: { version: number; cards: Card[]; hero?: string[]; dismissed?: string[] }[] = [];
	/** The saved keys of dismissed alerts. */
	dismissed: string[] = [];
	/** The last success of the Withings connection that needs reauthorization (fixed, so its alert key is stable). */
	reauthSince = '2026-09-20T10:00:00Z';
	/** The newest backup (fixed, so its alert key is stable). */
	backupAt = new Date(Date.now() - 10 * 24 * hour).toISOString();
	/** Delay of PUT /settings/dashboard in ms, to observe the optimistic hide. */
	putDelay = 0;
	/** Status of PUT /settings/dashboard; 503 simulates a failed save. */
	putStatus = 200;
	/** Query strings of GET /resolved/series and /resolved/trend, in order. */
	charts: string[] = [];
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
			return json(r, 200, { version: 1, cards: this.stored ?? defaultLayout, hero: this.storedHero ?? defaultHero, dismissed: this.dismissed, is_default: this.stored === null });
		}
		if (path === '/settings/dashboard' && method === 'PUT') {
			const body = r.request().postDataJSON() as { version: number; cards: Card[]; hero?: string[]; dismissed?: string[] };
			this.puts.push(body);
			if (this.putStatus !== 200) return problem(r, this.putStatus, 'unavailable', 'not ready');
			this.stored = body.cards;
			if (body.hero) this.storedHero = body.hero;
			this.dismissed = body.dismissed ?? [];
			const answer = { version: 1, cards: body.cards, hero: this.storedHero ?? defaultHero, dismissed: this.dismissed, is_default: false };
			return this.putDelay ? new Promise<void>((done) => setTimeout(done, this.putDelay)).then(() => json(r, 200, answer)) : json(r, 200, answer);
		}
		if (path === '/resolved/series') return this.series(r, url.searchParams);
		if (path === '/resolved/trend') return this.trend(r, url.searchParams);
		if (path === '/resolved/sleep') return this.sleep(r, url.searchParams);
		if (path === '/sleep') return this.sessions(r, url.searchParams);
		if (path === '/inventory') return this.inventory(r);
		if (path === '/metrics') return json(r, 200, { metrics: catalogue });
		if (path === '/resolved/summary') return this.summary(r, url.searchParams);
		if (path === '/providers') return json(r, 200, { providers });
		if (path === '/connections') {
			const list = this.empty ? [] : [connection('conn_' + 'a'.repeat(32), 'apple_health', { mode: 'push', official: null }), connection('conn_' + 'b'.repeat(32), 'withings', { status: 'needs_reauth', health: 'needs_reauth', consecutive_failures: 1, last_success_at: this.reauthSince })];
			return json(r, 200, { connections: list });
		}
		if (path === '/jobs') return json(r, 200, { jobs: [], has_more: false });
		if (path === '/system/status') return json(r, 200, { last_backup_at: this.empty ? null : this.backupAt });
		return r.fallback();
	}

	private summary(r: Route, q: URLSearchParams) {
		const date = q.get('date');
		this.summaryDates.push(date);
		const on = date ?? today();
		const metrics = q.getAll('metrics').flatMap((m) => m.split(',')).filter(Boolean);
		const compare = q.get('compare') === 'true';
		return json(r, 200, {
			date: on,
			timezone: 'Europe/Amsterdam',
			metrics: Object.fromEntries(metrics.map((m) => [m, { ...this.metric(m, on), ...(compare ? { comparisons: this.comparisons(m, on) } : {}) }]))
		});
	}

	/** The metrics with data, as inventory items: what edit mode offers beside the saved layout. */
	private inventory(r: Route) {
		const base = { count: 100, days: 90, first_date: '2026-06-01', last_date: today(), providers: [], devices: [], origins: [] };
		const items = this.empty
			? []
			: [
					...Object.keys(specs).map((code) => ({ ...base, kind: 'metric', code, metric: catalogue.find((m) => m.code === code) })),
					{ ...base, kind: 'sleep', code: 'sleep' },
					{ ...base, kind: 'group', code: 'bp_reading' },
					{ ...base, kind: 'event', code: 'irregular_rhythm' }
				];
		return json(r, 200, { items, aggregates_pending: false });
	}

	private has(code: string) {
		return !this.empty && (code in specs || code === 'sleep');
	}

	/** The 7/30/90/365-day periods ending at `end` beside the ones before them. */
	private comparisons(code: string, end: string) {
		const period = (to: string, days: number) => {
			const dates = Array.from({ length: days }, (_, i) => addDays(to, i - days + 1));
			return rollup(code, dates.map((d) => point(code, d)), days, to, !this.has(code));
		};
		return [7, 30, 90, 365].map((days) => ({ days, current: period(end, days), previous: period(addDays(end, -days), days) }));
	}

	// One resolved value per local date; every 13th date of a non-additive metric is a gap.
	private series(r: Route, q: URLSearchParams) {
		this.charts.push(`series ${q.toString()}`);
		const code = q.get('metric') ?? '';
		const [from, to] = [q.get('start')!.slice(0, 10), q.get('end')!.slice(0, 10)];
		const points = [];
		for (let d = from; d <= to && d <= today(); d = addDays(d, 1)) {
			if (!this.has(code)) continue;
			const n = Number(d.slice(5, 7)) * 31 + Number(d.slice(8, 10));
			const spec = specs[code];
			const end = `${addDays(d, 1)}T00:00:00Z`;
			if (spec?.agg !== 'additive' && n % 13 === 0) {
				points.push({ key: d, start: `${d}T00:00:00Z`, end, local_date: d, status: 'no_data', sources: [] });
				continue;
			}
			points.push({
				key: d, start: `${d}T00:00:00Z`, end, local_date: d, status: spec?.status ?? 'direct', value: point(code, d),
				...(d === today() && spec?.agg === 'additive' ? { partial: true } : {}),
				sources: [spec?.group ?? 'whoop'], providers: [spec?.provider ?? 'whoop']
			});
		}
		return json(r, 200, {
			metric: code, unit: specs[code]?.unit, window: { kind: q.get('window') ?? 'local_day' }, rule: { ref: `builtin:${code}:3`, version: 3 },
			timezone: 'Europe/Amsterdam', points, sources_used: points.length ? [specs[code]?.group ?? 'whoop'] : [], has_more: false
		});
	}

	// Weekly rollups from start_date through end_date.
	private trend(r: Route, q: URLSearchParams) {
		this.charts.push(`trend ${q.toString()}`);
		const code = q.get('metric') ?? '';
		const [from, to] = [q.get('start_date')!, q.get('end_date')!];
		const buckets = [];
		for (let d = from; d <= to; d = addDays(d, 7)) {
			const dates = Array.from({ length: 7 }, (_, i) => addDays(d, i)).filter((x) => x <= to);
			buckets.push(rollup(code, dates.map((x) => point(code, x)), dates.length, dates.at(-1)!, !this.has(code)));
		}
		return json(r, 200, { metric: code, unit: specs[code]?.unit, grain: 'week', timezone: 'Europe/Amsterdam', start_date: from, end_date: to, buckets });
	}

	// Last night: 23:10 to 06:40 UTC on WHOOP, with stages.
	private sleep(r: Route, q: URLSearchParams) {
		const d = q.get('end_date')!;
		if (this.empty) return json(r, 200, { timezone: 'UTC', nights: [] });
		const value = point('sleep', d);
		return json(r, 200, {
			timezone: 'UTC',
			nights: [{
				local_date: d,
				result: { status: 'direct', value, explanation: 'Synthetic night.' },
				episode: { start: `${addDays(d, -1)}T23:10:00Z`, end: `${d}T06:40:00Z` },
				members: [{ group: 'whoop', rule_status: 'used', selected: true, provider: 'whoop', session_refs: [`sleep-${d}`] }]
			}]
		});
	}

	private sessions(r: Route, q: URLSearchParams) {
		const d = q.get('end_date')!;
		const at = (h: number) => new Date(Date.parse(`${addDays(d, -1)}T23:10:00Z`) + h * hour).toISOString();
		const cycle = ['light', 'deep', 'light', 'rem', 'awake', 'light', 'deep', 'rem', 'light', 'awake'];
		const stages = cycle.map((stage, i) => ({ stage, start_at: at(i * 0.75), end_at: at((i + 1) * 0.75) }));
		const session = { id: `sleep-${d}`, start_at: at(0), end_at: at(7.5), tz_offset_min: 0, sleep_date: d, is_nap: false, has_stages: true, stages };
		return json(r, 200, { sleep: this.empty ? [] : [session], has_more: false });
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
