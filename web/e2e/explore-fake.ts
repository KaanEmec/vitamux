// Explore (J21.8) on top of data-fake.ts: the inventory, catalogue, summary, trend, per-source
// series, coverage and dashboard layout endpoints. Resolved days come from DataApi (with its
// override scenario on 2026-09-14), plus synthetic steps. The Day view (J26.3) reads intraday
// buckets and raw rows (heart rate: WHOOP every 6 s, Garmin every 2 min; steps: Apple Health per
// minute, Garmin per 15 min), one night and one workout. All values are synthetic.
import type { Page, Route } from '@playwright/test';
import { expect, fallbackDay, test as dataTest, type DataApi } from './data-fake';

export { fallbackDay };

type Json = Record<string, unknown>;

const metric = (code: string, section: string, unit: string, aggregation: string, group?: string, intraday?: { default: string; finest: string }) => ({
	code, section, unit, aggregation, group, kinds: ['sample'], windows: ['local_day'], strategies: ['first_available'],
	plausible_range: [0, 1000], provider_scoped: false, selection_only: false, intraday
});

export const catalogue = [
	metric('steps', 'Activity', 'count', 'additive', undefined, { default: '30m', finest: '1m' }),
	metric('heart_rate', 'Heart and circulation', 'bpm', 'intensive', undefined, { default: '1m', finest: 'raw' }),
	metric('resting_heart_rate', 'Heart and circulation', 'bpm', 'daily_summary'),
	metric('bp_systolic', 'Blood pressure', 'mmHg', 'latest', 'bp_reading'),
	metric('spo2', 'Respiration and oxygen', '%', 'intensive'),
	metric('weight', 'Body composition', 'kg', 'latest', 'body_composition')
];
const meta = (code: string) => catalogue.find((m) => m.code === code);

const watch = { id: 'dev_1', type: 'watch', model: 'Synthetic Watch' };
const scale = { id: 'dev_2', type: 'scale', model: 'Synthetic Scale' };
const phoneApp = { key: 'com.example.health', name: 'Example Health' };

const item = (kind: string, code: string, extra: Json): Json => ({
	kind, code, count: 120, days: 90, first_date: '2026-06-18', last_date: '2026-09-16', first_at: '2026-06-18T06:00:00Z', last_at: '2026-09-16T06:00:00Z',
	providers: [], devices: [], origins: [], ...extra
});

const inventory = [
	item('metric', 'steps', { metric: meta('steps'), providers: ['apple_health', 'garmin'], devices: [watch], origins: [phoneApp], latest: { local_date: '2026-09-16', value: 9412, unit: 'count' } }),
	item('metric', 'heart_rate', { metric: meta('heart_rate'), providers: ['garmin', 'apple_health'], devices: [watch], latest: { local_date: '2026-09-16', value: 64, unit: 'bpm' } }),
	item('metric', 'resting_heart_rate', { metric: meta('resting_heart_rate'), providers: ['whoop', 'garmin', 'apple_health'], devices: [watch], latest: { local_date: '2026-09-16', value: 50, unit: 'bpm' } }),
	item('metric', 'weight', { metric: meta('weight'), providers: ['withings'], devices: [scale], days: 12, latest: { local_date: '2026-09-15', value: 72.4, unit: 'kg' } }),
	item('group', 'bp_reading', { components: ['bp_systolic', 'bp_diastolic', 'bp_pulse'], providers: ['withings'], latest: { local_date: '2026-09-15', components: { bp_systolic: 121, bp_diastolic: 79 } } }),
	item('sleep', 'sleep', { providers: ['garmin'], devices: [watch], origins: [{ key: 'com.example.sleep', name: 'Example Sleep' }], latest: { local_date: '2026-09-16', value: 26100 } }),
	item('workouts', 'workouts', { providers: ['garmin'], latest: { local_date: '2026-09-14', text: 'running' } }),
	item('event', 'irregular_rhythm', { providers: ['apple_health'], origins: [phoneApp], days: 2, latest: { local_date: '2026-09-02', level: 'low' } }),
	item('analyte', 'ldl_c', { analyte: { code: 'ldl_c', name: 'LDL cholesterol', canonical_unit: 'mmol/L' }, days: 3, latest: { local_date: '2026-08-01', value: 2.9, unit: 'mmol/L', text: '2.9 mmol/L' } })
];

/** Synthetic steps per day, by date. */
const steps = (date: string) => 6000 + (Number(date.slice(-2)) % 7) * 900;

export class ExploreApi {
	layout = { version: 1, cards: [{ metric: 'steps', size: 'M', hidden: false }], is_default: true };
	/** Bodies of PUT /settings/dashboard. */
	saved: Json[] = [];
	/** GET /sources/series answers no sources. */
	noSources = false;
	/** Day view reads, in order: `resolved <window>` and `sources <grain>`. */
	intraday: string[] = [];

	constructor(
		private page: Page,
		private data: DataApi
	) {}

	async install() {
		const resolve = this.data.resolve.bind(this.data);
		this.data.resolve = (m, date) =>
			m === 'steps'
				? { status: 'direct', value: steps(date), unit: 'count', window: { kind: 'local_day', local_date: date }, rule: { ref: 'builtin:steps:4', version: 4, strategy: 'first_available' }, selected: 'apple_watch', inputs: [], explanation: `First available source: apple_watch ${steps(date)} steps.` }
				: resolve(m, date);
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const q = url.searchParams;
		let m: RegExpMatchArray | null;
		if (path === '/inventory') return json(r, 200, { items: inventory, aggregates_pending: false });
		if (path === '/metrics') return json(r, 200, { metrics: catalogue });
		if ((m = path.match(/^\/metrics\/([^/]+)$/))) {
			const found = meta(m[1]);
			return found ? json(r, 200, found) : problem(r, 404, 'not_found', 'no such metric');
		}
		if (path === '/resolved/summary') return this.summary(r, q);
		if (path === '/resolved/trend') return this.trend(r, q);
		if (path === '/sources/series') return intradayGrains.includes(q.get('grain') ?? '') ? this.fine(r, q) : this.sources(r, q);
		if (path === '/resolved/series') return this.buckets(r, q);
		if (path === '/resolved/sleep') return json(r, 200, { timezone: 'Europe/Amsterdam', nights: [night] });
		if (path === '/resolved/workouts') return json(r, 200, { timezone: 'Europe/Amsterdam', rule: { ref: 'builtin:workouts:1', version: 1 }, workouts: [workout] });
		if (path === '/coverage') return this.coverage(r, q);
		if (path === '/settings/dashboard' && r.request().method() === 'PUT') {
			const body = r.request().postDataJSON() as Json;
			this.saved.push(body);
			this.layout = { ...(body as typeof this.layout), is_default: false };
			return json(r, 200, this.layout);
		}
		if (path === '/settings/dashboard') return json(r, 200, this.layout);
		return r.fallback();
	}

	private summary(r: Route, q: URLSearchParams) {
		if (this.data.resolvedStatus !== 200) return problem(r, this.data.resolvedStatus, 'unavailable', 'resolved values are not ready yet');
		const date = q.get('date') ?? '2026-09-16';
		const codes = q.getAll('metrics').flatMap((v) => v.split(','));
		const metrics = Object.fromEntries(
			codes.map((code) => {
				const days = Array.from({ length: 30 }, (_, i) => addDays(date, i - 29));
				const value = this.data.resolve(code, date) as Json;
				const sparkline = days.map((d) => {
					const v = this.data.resolve(code, d) as Json;
					return { local_date: d, status: v.status, value: v.value };
				});
				const rollup = (n: number, to = date, mean = 51) => ({ start_date: addDays(to, 1 - n), end_date: to, days: n, n: n - 1, coverage: (n - 1) / n, mean, min: 48, max: 55 });
				// With compare=true: each period beside the one before it (previous mean 49).
				const comparisons = q.get('compare') === 'true' ? [7, 30, 90, 365].map((n) => ({ days: n, current: rollup(n), previous: rollup(n, addDays(date, -n), 49) })) : undefined;
				return [code, { metric: code, unit: meta(code)?.unit, rule: value.rule, value, sparkline, stats: [rollup(7), rollup(30), rollup(90)], comparisons }];
			})
		);
		return json(r, 200, { date, timezone: 'Europe/Amsterdam', metrics });
	}

	private trend(r: Route, q: URLSearchParams) {
		const [start, end] = [q.get('start_date')!, q.get('end_date')!];
		const grain = q.get('grain') ?? 'week';
		const buckets = [];
		for (let d = start; d <= end; ) {
			const next = grain === 'week' ? addDays(d, 7) : `${nextMonth(d)}`;
			const last = addDays(next, -1) < end ? addDays(next, -1) : end;
			const has = d >= '2026-06-01';
			buckets.push({ start_date: d, end_date: last, days: 7, n: has ? 6 : 0, coverage: has ? 0.86 : 0, ...(has ? { mean: 51, min: 47, max: 56 } : {}) });
			d = next;
		}
		return json(r, 200, { metric: q.get('metric'), unit: 'bpm', grain, timezone: 'Europe/Amsterdam', start_date: start, end_date: end, buckets });
	}

	private sources(r: Route, q: URLSearchParams) {
		const [start, end] = [q.get('start')!.slice(0, 10), q.get('end')!.slice(0, 10)];
		const source = (provider: string, base: number) => {
			const points = [];
			for (let d = start; d < end; d = addDays(d, 1)) points.push({ local_date: d, n: 24, mean: base + (Number(d.slice(-2)) % 3), daily_value: base });
			return { provider, connection_id: `conn_${provider}`, device: { type: 'watch' }, group: provider, rule_status: 'used', points };
		};
		return json(r, 200, {
			metric: q.get('metric'), unit: 'bpm', aggregation: 'daily_summary', grain: 'day', timezone: 'Europe/Amsterdam', behind: false,
			sources: this.noSources ? [] : [source('garmin', 52), source('apple_health', 54)]
		});
	}

	/** Resolved buckets of the requested size over [start, end). */
	private buckets(r: Route, q: URLSearchParams) {
		const size = q.get('window') ?? '1m';
		this.intraday.push(`resolved ${size}`);
		const steps = q.get('metric') === 'steps';
		const ms = seconds[size] * 1000;
		const points = [];
		for (let t = Date.parse(q.get('start')!); t < Date.parse(q.get('end')!); t += ms) {
			const v = steps ? 150 + (t / ms) % 7 * 20 : hr(t);
			points.push({
				key: new Date(t).toISOString(), start: new Date(t).toISOString(), end: new Date(t + ms).toISOString(), local_date: '2026-09-14', status: 'direct',
				value: v, n: steps ? 1 : Math.max(1, ms / 6000), coverage: 1, sources: [steps ? 'apple_health' : 'whoop'], providers: [steps ? 'apple_health' : 'whoop'],
				...(steps ? {} : { min: v - 4, max: v + 5 })
			});
		}
		return json(r, 200, { metric: q.get('metric'), unit: steps ? 'count' : 'bpm', window: { kind: 'bucket', size }, rule: { ref: 'builtin:x:1', version: 1 }, timezone: 'Europe/Amsterdam', points, sources_used: [], has_more: false });
	}

	/** Each source at an intraday grain (never finer than it was sent) or its raw rows. */
	private fine(r: Route, q: URLSearchParams) {
		const grain = q.get('grain')!;
		this.intraday.push(`sources ${grain}`);
		const steps = q.get('metric') === 'steps';
		const [from, to] = [Date.parse(q.get('start')!), Date.parse(q.get('end')!)];
		const source = (provider: string, spacing: number, device: Json, origin?: Json) => {
			const ms = grain === 'raw' ? spacing * 1000 : Math.max(seconds[grain], spacing) * 1000;
			const points = [];
			for (let t = Math.ceil(from / ms) * ms; t < to; t += ms) {
				const v = steps ? 20 * (ms / 60000) : hr(t) + (provider === 'garmin' ? 3 : 0);
				points.push({ start: new Date(t).toISOString(), local_date: '2026-09-14', n: 1, ...(grain === 'raw' ? { value: v } : steps ? { sum: v } : { mean: v, min: v - 2, max: v + 2 }) });
			}
			return { provider, connection_id: `conn_${provider}`, device, origin, group: provider, rule_status: 'used', spacing_s: spacing, points };
		};
		const sources = steps
			? [source('apple_health', 60, watch, phoneApp), source('garmin', 900, watch)]
			: [source('whoop', 6, { id: 'dev_3', type: 'band', model: 'Synthetic Band' }), source('garmin', 120, watch)];
		return json(r, 200, { metric: q.get('metric'), unit: steps ? 'count' : 'bpm', aggregation: steps ? 'additive' : 'intensive', grain, timezone: 'Europe/Amsterdam', behind: false, sources, has_more: false });
	}

	private coverage(r: Route, q: URLSearchParams) {
		const [start, end] = [q.get('start_date')!, q.get('end_date')!];
		let n = 0;
		for (let d = start; d <= end; d = addDays(d, 1)) n++;
		const rows = ['garmin', 'apple_health'].map((source, k) => ({ metric: q.getAll('metric')[0], source, days: Array.from({ length: n }, (_, i) => ((i + k) % 5 ? 0.9 : 0)) }));
		return json(r, 200, { start_date: start, end_date: end, rows });
	}
}

const intradayGrains = ['30s', '1m', '5m', '15m', '30m', 'raw'];
const seconds: Record<string, number> = { '30s': 30, '1m': 60, '5m': 300, '15m': 900, '30m': 1800 };
/** Synthetic heart rate at an instant. */
const hr = (t: number) => Math.round(62 + 8 * Math.sin(t / 3_600_000));
/** The night before 2026-09-14 (23:30–07:00 in Amsterdam) and an evening run (18:00–19:00). */
const night = { local_date: '2026-09-14', result: { status: 'direct', explanation: '' }, episode: { start: '2026-09-13T21:30:00Z', end: '2026-09-14T05:00:00Z' }, members: [] };
const workout = { local_date: '2026-09-14', start: '2026-09-14T16:00:00Z', end: '2026-09-14T17:00:00Z', sport: 'running', selected: null, explanation: 'Synthetic run.', members: [] };

function addDays(d: string, n: number): string {
	return new Date(Date.parse(`${d}T00:00:00Z`) + n * 86_400_000).toISOString().slice(0, 10);
}
function nextMonth(d: string): string {
	const t = new Date(`${d.slice(0, 7)}-01T00:00:00Z`);
	t.setUTCMonth(t.getUTCMonth() + 1);
	return t.toISOString().slice(0, 10);
}
function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string) {
	return r.fulfill({ status, contentType: 'application/problem+json', body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e' }) });
}

/** `test` with data-fake's DataApi plus a fresh ExploreApi per test. */
export const test = dataTest.extend<{ explore: ExploreApi }>({
	explore: [
		async ({ page, data }, use) => {
			const explore = new ExploreApi(page, data);
			await explore.install();
			await use(explore);
		},
		{ auto: true }
	]
});

export { expect };
