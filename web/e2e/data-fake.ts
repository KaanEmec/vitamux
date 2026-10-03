// A stateful stand-in for the resolved, source-data and override endpoints used by the
// Data section (J11.3), on top of fake-api.ts. All values are synthetic.
//
// Scenario "resting_heart_rate" on 2026-09-14: the preferred source (whoop) has a degraded
// stream, so the day falls back to garmin (52); excluding garmin's record falls back again
// to apple_watch (54). Scenario "heart_rate" on the same day carries 14,400 samples for the
// render-time budget. Overrides are applied by `resolve` below the way the engine would.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
interface Override {
	id: string;
	metric: string;
	window: { kind: string; key: string; local_date: string };
	action: 'exclude_input' | 'force_source' | 'set_value';
	input_id: string | null;
	group: string | null;
	value: number | null;
	unit: string | null;
	note: string | null;
	active: boolean;
	created_by: string;
	created_at: string;
	revoked_by: string | null;
	revoked_at: string | null;
}

export const fallbackDay = '2026-09-14';
const dayStart = Date.parse('2026-09-13T22:00:00Z'); // 2026-09-14T00:00:00+02:00

// Candidate inputs in rule order. `record` is the measurement id the input's value comes from.
const ladder = [
	{ group: 'whoop', value: null as number | null, record: null as string | null, reason: 'stream degraded: schema_drift since 2026-09-13T06:00Z' },
	{ group: 'garmin', value: 52, record: '9182736', reason: '' },
	{ group: 'apple_watch', value: 54, record: '9182740', reason: '' }
];

const sourcesList = [
	{ group: 'garmin', provider: 'garmin', conn: 'conn_garmin', device: 'watch' },
	{ group: 'apple_watch', provider: 'apple_health', conn: 'conn_apple', device: 'watch' }
];

export class DataApi {
	/** Status of GET /resolved/daily; 503 simulates the endpoint not being ready. */
	resolvedStatus = 200;
	overrides: Override[] = [];
	/** Bodies of POST /overrides, in order. */
	created: Json[] = [];
	private nextId = 1;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const method = r.request().method();
		const q = url.searchParams;
		let m: RegExpMatchArray | null;
		if (path === '/metrics') return json(r, 200, { metrics: ['steps', 'resting_heart_rate', 'heart_rate'].map((code) => ({ code, windows: ['local_day'] })) });
		if (path === '/resolved/daily') return this.daily(r, q);
		if ((m = path.match(/^\/resolved\/([^/]+)\/([^/]+)\/sources$/))) return this.sources(r, m[1], m[2]);
		if (path === '/measurements') return this.measurements(r, q);
		if (path === '/overrides' && method === 'GET') {
			const metrics = q.getAll('metric');
			return json(r, 200, { overrides: this.overrides.filter((o) => !metrics.length || metrics.includes(o.metric)), has_more: false });
		}
		if (path === '/overrides' && method === 'POST') return this.create(r);
		if ((m = path.match(/^\/overrides\/([^/]+)\/revoke$/)) && method === 'POST') {
			const o = this.overrides.find((x) => x.id === m![1]);
			if (!o) return problem(r, 404, 'not_found', 'no such override');
			o.active = false;
			o.revoked_by = 'owner';
			o.revoked_at = '2026-09-15T08:00:00Z';
			return json(r, 200, o);
		}
		if ((m = path.match(/^\/provenance\/(measurement|sleep|workout|group)\/([^/]+)$/))) return json(r, 200, provenance(m[1], m[2]));
		if (path === '/sleep') return json(r, 200, { sleep: sleepSessions, has_more: false });
		if (path === '/workouts') return json(r, 200, { workouts: workouts, has_more: false });
		return r.fallback();
	}

	// ---- resolution -------------------------------------------------------------------
	private active(metric: string, date: string) {
		return this.overrides.filter((o) => o.active && o.metric === metric && o.window.local_date === date);
	}

	/** The result of one metric and day, with the overrides applied. */
	resolve(metric: string, date: string): Json {
		if (metric === 'heart_rate') {
			return {
				status: 'calculated', value: 61.4, unit: 'bpm', window: { kind: 'local_day', local_date: date },
				rule: { ref: 'builtin:heart_rate', version: 1, strategy: 'mean_across_sources' },
				inputs: [
					{ group: 'garmin', status: 'used', selected: true, value: 61.8, basis: 'samples', coverage: 0.98, record_refs: ['7000001'] },
					{ group: 'apple_watch', status: 'used', selected: true, value: 61, basis: 'samples', coverage: 0.9, record_refs: ['7100001'] }
				],
				explanation: 'Mean of 2 eligible sources: Garmin 61.8 bpm; Apple Watch 61 bpm.', computed_at: '2026-09-15T06:00:03Z'
			};
		}
		if (metric !== 'resting_heart_rate') {
			return { status: 'no_data', explanation: 'No source had a value.', window: { kind: 'local_day', local_date: date } };
		}
		const base = { unit: 'bpm', window: { kind: 'local_day', local_date: date }, rule: { ref: 'builtin:resting_heart_rate', version: 1, strategy: 'first_available' }, computed_at: '2026-09-15T06:00:03Z' };
		if (date !== fallbackDay) {
			return {
				...base, status: 'direct', value: 50,
				inputs: [{ group: 'whoop', status: 'used', selected: true, value: 50, basis: 'daily_value', coverage: 1, record_refs: ['9100000'] }],
				explanation: 'First available source: WHOOP 50 bpm.'
			};
		}
		const live = this.active(metric, date);
		const excluded = live.filter((o) => o.action === 'exclude_input').map((o) => o.input_id);
		const forced = live.find((o) => o.action === 'force_source')?.group;
		const set = live.find((o) => o.action === 'set_value');
		const inputs = ladder.map((l) => {
			if (l.value == null) return { group: l.group, status: 'no_data', reason: l.reason };
			if (excluded.includes(l.record)) return { group: l.group, status: 'excluded', reason: `record ${l.record} excluded by override` };
			return { group: l.group, status: 'used', selected: false, value: l.value, basis: 'daily_value', coverage: 1, record_refs: [l.record] };
		});
		const valid = inputs.filter((i) => i.status === 'used');
		const pick = (forced && valid.find((i) => i.group === forced)) || valid[0];
		for (const i of inputs) if (i === pick) i.selected = true;
		if (set) {
			return { ...base, status: 'overridden', value: set.value, unit: set.unit ?? 'bpm', inputs, explanation: `Set manually to ${set.value} ${set.unit}: ${set.note}. Computed value kept.` };
		}
		if (!pick) return { ...base, status: 'no_data', inputs, explanation: 'No source had a value.' };
		const first = inputs[0];
		return {
			...base, status: pick === first ? 'direct' : 'fallback', value: pick.value, inputs,
			explanation: `${first.group === pick.group ? '' : 'whoop had no value (stream degraded). '}Fell back to ${pick.group}: ${pick.value} bpm.`
		};
	}

	private daily(r: Route, q: URLSearchParams) {
		if (this.resolvedStatus !== 200) return problem(r, this.resolvedStatus, 'unavailable', 'resolved values are not ready yet');
		const metrics = (q.get('metrics') ?? '').split(',').filter(Boolean);
		const days = [];
		for (let d = q.get('start_date')!; d <= q.get('end_date')!; d = addDay(d)) {
			days.push({ local_date: d, metrics: Object.fromEntries(metrics.map((m) => [m, this.resolve(m, d)])) });
		}
		return json(r, 200, { timezone: 'Europe/Amsterdam', days });
	}

	private sources(r: Route, metric: string, date: string) {
		return json(r, 200, {
			metric, window: { kind: 'local_day', local_date: date }, rule: { ref: `builtin:${metric}`, version: 1 },
			sources: [
				...sourcesList.map((s) => ({
					group: s.group, rule_status: 'used', provider: s.provider, device: { type: s.device },
					values: metric === 'heart_rate' ? { samples: s.group === 'garmin' ? 14400 : 1440 } : { daily_value: s.group === 'garmin' ? 52 : 54 },
					records: { href: `/api/v1/measurements?metric=${metric}&connection=${s.conn}&start_date=${date}&end_date=${date}` },
					provenance: { raw_payload_ids: ['4411'], normalizer: `${s.provider}.daily_summary@3`, fetched_at: '2026-09-14T21:02:11Z' }
				})),
				{ group: null, rule_status: 'excluded', reason: 'exclude: relayed=true', provider: 'apple_health', origin: { key: 'com.garmin.connect.mobile', relayed_provider: 'garmin' }, values: { interval_sum: 11288 } }
			]
		});
	}

	private measurements(r: Route, q: URLSearchParams) {
		const metric = q.getAll('metric')[0];
		const conn = q.getAll('connection')[0];
		const limit = Number(q.get('limit') ?? 500);
		const offset = q.get('cursor') ? Number(atob(q.get('cursor')!)) : 0;
		const total = metric === 'heart_rate' ? (conn === 'conn_garmin' ? 14400 : conn === 'conn_apple' ? 1440 : 0) : conn ? 1 : 0;
		const step = metric === 'heart_rate' ? (conn === 'conn_garmin' ? 6 : 60) : 0;
		const items = [];
		for (let i = offset; i < Math.min(offset + limit, total); i++) {
			const base = conn === 'conn_garmin' ? 62 : 60;
			items.push({
				id: metric === 'heart_rate' ? String((conn === 'conn_garmin' ? 7000001 : 7100001) + i) : conn === 'conn_garmin' ? '9182736' : '9182740',
				metric, kind: metric === 'heart_rate' ? 'sample' : 'daily_value',
				start_at: new Date(dayStart + i * step * 1000).toISOString(), end_at: null, tz_offset_min: 120, local_date: fallbackDay,
				value: metric === 'heart_rate' ? Math.round((base + 12 * Math.sin(i / 400) + (i % 7)) * 10) / 10 : conn === 'conn_garmin' ? 52 : 54,
				unit: 'bpm', source_value: null, source_unit: null, quality_flags: 0, group_id: null,
				source: { provider: conn === 'conn_garmin' ? 'garmin' : 'apple_health', connection_id: conn, device: null, device_type: 'watch', origin: null, external_id: null, dedupe_key: '0'.repeat(32) },
				provenance: { raw_payload_id: '4411', normalizer: 'garmin.daily_summary@3', ingested_at: '2026-09-14T21:02:11Z', normalized_at: '2026-09-14T21:02:12Z', superseded_at: null, superseded_by: null, deleted_at: null, deleted_by_raw_id: null }
			});
		}
		const end = offset + limit;
		return json(r, 200, { measurements: items, has_more: end < total, ...(end < total ? { next_cursor: btoa(String(end)) } : {}) });
	}

	private create(r: Route) {
		const body = r.request().postDataJSON() as Json & { window: Override['window'] };
		this.created.push(body);
		if (body.action === 'exclude_input' && !body.input_id) {
			return problem(r, 422, 'validation_failed', 'invalid override', [{ pointer: '/input_id', detail: 'is required for exclude_input' }]);
		}
		const o: Override = {
			id: `00000000-0000-4000-8000-0000000000${String(this.nextId++).padStart(2, '0')}`,
			metric: body.metric as string, window: body.window, action: body.action as Override['action'],
			input_id: (body.input_id as string) ?? null, group: (body.group as string) ?? null,
			value: (body.value as number) ?? null, unit: (body.unit as string) ?? null, note: (body.note as string) ?? null,
			active: true, created_by: 'owner', created_at: '2026-09-15T07:00:00Z', revoked_by: null, revoked_at: null
		};
		this.overrides.push(o);
		return json(r, 201, o);
	}
}

function addDay(d: string): string {
	const t = new Date(Date.parse(d + 'T00:00:00Z') + 86_400_000);
	return t.toISOString().slice(0, 10);
}

function provenance(entity: string, id: string): Json {
	const version = (vid: string, superseded: boolean) => ({
		id: vid, superseded_by: superseded ? id : null, record: { id: vid, metric: 'resting_heart_rate', value: superseded ? 53 : 52 },
		provider: 'garmin', connection_id: 'conn_garmin', connection_mode: 'sync', client: null,
		batch: { id: '11111111-1111-4111-8111-111111111111', source_kind: 'sync', migration_source: null, received_at: '2026-09-14T21:02:11Z' },
		raw: { id: '4411', stream: 'daily_summary', external_key: '2026-09-14', version: 1, content_sha256: 'ab'.repeat(32), content_type: 'application/json', size_bytes: 2048, fetched_at: '2026-09-14T21:02:11Z', stored_at: '2026-09-14T21:02:11Z', request_meta: {}, shape_fingerprint: 'fp', status: 'normalized' },
		normalizer: { name: 'garmin.daily_summary', version: 3, git_sha: 'abcdef0123456789' },
		fetched_at: '2026-09-14T21:02:11Z', ingested_at: '2026-09-14T21:02:11Z', normalized_at: '2026-09-14T21:02:12Z',
		corrected_at: superseded ? null : '2026-09-14T22:00:00Z', superseded_at: superseded ? '2026-09-14T22:00:00Z' : null, deleted_at: null, deleted_by: null
	});
	return { entity, row: version(id, false), earlier: [version('9182000', true)], later: [] };
}

// ---- sleep and workouts -------------------------------------------------------------
const stage = (stageName: string, from: string, to: string) => ({ stage: stageName, start_at: from, end_at: to });
const sleepSource = (provider: string, origin: string | null) => ({ provider, connection_id: `conn_${provider}`, device: null, device_type: 'watch', origin, external_id: null, dedupe_key: '0'.repeat(32) });
const sleepProv = { raw_payload_id: '5', normalizer: 'sleep@1', ingested_at: '2026-09-14T06:00:00Z', normalized_at: '2026-09-14T06:00:01Z', superseded_at: null, superseded_by: null, deleted_at: null, deleted_by_raw_id: null };
const sleepSessions = [
	{
		id: '22222222-2222-4222-8222-222222222221', start_at: '2026-09-13T21:10:00Z', end_at: '2026-09-14T04:55:00Z', tz_offset_min: 120,
		sleep_date: '2026-09-14', is_nap: false, has_stages: true, totals_basis: 'provider', asleep_s: 26100, deep_s: 5400, light_s: 14400, rem_s: 6300, awake_s: 1500, latency_s: 600,
		stages: [stage('light', '2026-09-13T21:10:00Z', '2026-09-13T22:30:00Z'), stage('deep', '2026-09-13T22:30:00Z', '2026-09-14T00:00:00Z'), stage('rem', '2026-09-14T00:00:00Z', '2026-09-14T01:00:00Z'), stage('awake', '2026-09-14T01:00:00Z', '2026-09-14T01:10:00Z'), stage('light', '2026-09-14T01:10:00Z', '2026-09-14T04:55:00Z')],
		source: sleepSource('garmin', null), provenance: sleepProv
	},
	{
		id: '22222222-2222-4222-8222-222222222222', start_at: '2026-09-13T21:40:00Z', end_at: '2026-09-14T05:05:00Z', tz_offset_min: 120,
		sleep_date: '2026-09-14', is_nap: false, has_stages: true, totals_basis: 'stages', asleep_s: 25200, deep_s: 4800, light_s: 15000, rem_s: 5400, awake_s: 1500, latency_s: null,
		stages: [stage('light', '2026-09-13T21:40:00Z', '2026-09-13T23:00:00Z'), stage('deep', '2026-09-13T23:00:00Z', '2026-09-14T00:20:00Z'), stage('rem', '2026-09-14T00:20:00Z', '2026-09-14T01:20:00Z'), stage('light', '2026-09-14T01:20:00Z', '2026-09-14T05:05:00Z')],
		source: sleepSource('apple_health', 'com.apple.health'), provenance: sleepProv
	}
];

const workout = (id: string, provider: string, start: string, end: string, sport: string, km: number) => ({
	id, start_at: start, end_at: end, tz_offset_min: 120, local_date: '2026-09-14', sport, provider_sport: sport, distance_m: km * 1000, energy_kcal: 410, avg_hr_bpm: 148, max_hr_bpm: 171, file_sha256: null,
	source: sleepSource(provider, null), provenance: sleepProv
});
const workouts = [
	workout('33333333-3333-4333-8333-333333333331', 'garmin', '2026-09-14T05:10:00Z', '2026-09-14T05:52:00Z', 'running', 8.1),
	workout('33333333-3333-4333-8333-333333333332', 'apple_health', '2026-09-14T05:11:00Z', '2026-09-14T05:51:00Z', 'running', 8),
	workout('33333333-3333-4333-8333-333333333333', 'garmin', '2026-09-14T16:00:00Z', '2026-09-14T16:45:00Z', 'cycling', 15)
];

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
	return r.fulfill({
		status, contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e', errors })
	});
}

/** `test` with the base FakeApi (signed in) plus a fresh DataApi per test. */
export const test = base.extend<{ data: DataApi }>({
	data: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const data = new DataApi(page);
			await data.install();
			await use(data);
		},
		{ auto: true }
	]
});

export { expect };
