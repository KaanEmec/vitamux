// The Apple Watch views (J22.18) on top of views-fake.ts (clock fixed at 2026-09-14 noon UTC,
// records at +02:00), on the J22.17 endpoints and codes (docs/adr/0024-watch-data.md). The shapes
// follow VitamuxKit's FakeServer+Watch.swift; every value is synthetic and the route sits at sea
// near 0°, 0°.
//
// - ECG: three recordings. 2026-09-14 (sinus rhythm, 64 bpm) and 2026-08-25 (atrial fibrillation,
//   symptoms present) have a 30 s waveform at 512 Hz; 2026-09-10 (inconclusive, no average) has none.
// - Other new event families: an irregular rhythm alert, State of Mind and a mindful session,
//   menstrual flow and a headache symptom, alongside the views fixture's audio alerts.
// - Beats: two heartbeat series a day (07:00 and 14:30, 89 intervals each), none on 2026-09-11.
// - Activity summaries: 60 days of move, exercise and stand with Apple's goals; 2026-09-12 paused.
// - Workouts: the Apple Health run (w-run-apple) has laps, a pause, a marker and a route; the ride
//   (w-ride) two activities and no route; the walk (w-walk) neither.
import type { Page, Route } from '@playwright/test';
import { events as baseEvents, expect, test as viewsTest } from './views-fake';

type Json = Record<string, unknown>;

export const today = '2026-09-14';
export const beatsGap = '2026-09-11';
export const pausedDay = '2026-09-12';
const offset = 120;
const summaryDays = 60;

const addDay = (d: string, n: number) => new Date(Date.parse(`${d}T00:00:00Z`) + n * 86_400_000).toISOString().slice(0, 10);
/** UTC ms of a local wall-clock time at +02:00. */
const at = (date: string, minutes: number) => Date.parse(`${date}T00:00:00Z`) + (minutes - offset) * 60_000;
const iso = (t: number) => new Date(t).toISOString();
const dayNumber = (d: string) => Math.round(Date.parse(`${d}T00:00:00Z`) / 86_400_000);

const provenance = { raw_payload_id: '7', normalizer: 'apple_health@4', ingested_at: '2026-09-14T06:00:00Z', normalized_at: '2026-09-14T06:00:01Z', superseded_at: null, superseded_by: null, deleted_at: null, deleted_by_raw_id: null };
const source = (over: Json = {}) => ({ provider: 'apple_health', connection_id: 'conn_apple_health', device: 'dev_watch', device_type: 'watch', origin: null, external_id: null, dedupe_key: '0'.repeat(32), ...over });

export const ecgIds = {
	withWaveform: '00000000-0000-4000-8000-000000000103',
	older: '00000000-0000-4000-8000-000000000101',
	noWaveform: '00000000-0000-4000-8000-000000000102'
};
const withWaveform = new Set([ecgIds.withWaveform, ecgIds.older]);

interface Plan {
	id: string;
	code: string;
	date: string;
	minutes: number;
	length?: number;
	level?: string;
	value?: number;
	context?: Json;
}

const plans: Plan[] = [
	{ id: ecgIds.older, code: 'ecg_recording', date: '2026-08-25', minutes: 12 * 60 + 15, level: 'atrial_fibrillation', value: 88, context: { symptoms_status: 'present', voltage_count: 15360, sampling_frequency_hz: 512, lead: 'apple_watch_similar_to_lead_i', algorithm_version: 2 } },
	{ id: 'ev-cycle', code: 'menstrual_flow', date: '2026-09-01', minutes: 8 * 60, length: 24 * 60, level: 'medium' },
	{ id: 'ev-rhythm', code: 'irregular_rhythm_alert', date: '2026-09-05', minutes: 3 * 60 + 10 },
	{ id: 'ev-symptom', code: 'symptom_headache', date: '2026-09-08', minutes: 16 * 60, length: 120, level: 'mild' },
	{ id: ecgIds.noWaveform, code: 'ecg_recording', date: '2026-09-10', minutes: 21 * 60 + 40, level: 'inconclusive_poor_reading', context: { symptoms_status: 'not_set', voltage_count: 0 } },
	{ id: 'ev-mindful', code: 'mindful_session', date: '2026-09-12', minutes: 22 * 60, length: 5 },
	{ id: 'ev-mood', code: 'state_of_mind', date: '2026-09-13', minutes: 21 * 60, level: 'daily_mood', value: -0.2, context: { valence_classification: 'slightly_unpleasant', labels: ['drained'] } },
	{ id: ecgIds.withWaveform, code: 'ecg_recording', date: today, minutes: 8 * 60 + 5, level: 'sinus_rhythm', value: 64, context: { symptoms_status: 'none', voltage_count: 15360, sampling_frequency_hz: 512, lead: 'apple_watch_similar_to_lead_i', algorithm_version: 2 } }
];

const watchEvents = plans.map((p) => {
	const start = at(p.date, p.minutes);
	const end = p.code === 'ecg_recording' ? start + 30_000 : p.length ? start + p.length * 60_000 : null;
	return {
		id: p.id, code: p.code, start_at: iso(start), end_at: end == null ? null : iso(end), tz_offset_min: offset, local_date: p.date,
		value: p.value ?? null, level: p.level ?? null, context: p.context ?? {}, quality_flags: 0,
		...(withWaveform.has(p.id) ? { file_sha256: p.id.replaceAll('-', '').padEnd(64, '0') } : {}),
		source: source(), provenance
	};
});

/** vitamux.waveform/1: a synthetic beat (P, QRS, T) every 60/hr seconds, in µV. */
function waveform(id: string): Json | null {
	const p = plans.find((x) => x.id === id);
	if (!p || !withWaveform.has(id)) return null;
	const hz = 512;
	const period = 60 / (p.value ?? 60);
	const values = Array.from({ length: hz * 30 }, (_, i) => {
		const t = i / hz;
		const phase = ((t + 0.4) % period) - 0.4;
		const bump = (c: number, w: number, h: number) => h * Math.exp(-(((phase - c) / w) ** 2));
		return Math.round(bump(-0.2, 0.025, 120) + bump(-0.03, 0.008, -90) + bump(0, 0.01, 950) + bump(0.03, 0.009, -230) + bump(0.28, 0.05, 280) + 12 * Math.sin(t * 1.7));
	});
	return { format: 'vitamux.waveform/1', start: iso(at(p.date, p.minutes)), sampling_frequency_hz: hz, unit: 'µV', lead: 'apple_watch_similar_to_lead_i', values };
}

const measurement = (id: string, metric: string, kind: string, start: number, end: number | null, date: string, value: number, unit: string, src: Json, context?: Json) => ({
	id, metric, kind, start_at: iso(start), end_at: end == null ? null : iso(end), tz_offset_min: offset, local_date: date, value, unit,
	source_value: null, source_unit: null, quality_flags: 0, group_id: null, ...(context ? { context } : {}), source: source(src), provenance
});

/** Two heartbeat series on the day, one row per beat after the first, keyed `<series>#<beat>`. */
function beats(date: string) {
	if (date === beatsGap || date > today || date <= addDay(today, -30)) return [];
	return [1, 2].flatMap((series) => {
		const uuid = `00000000-0000-4000-9000-${String(series).padStart(5, '0')}${String(dayNumber(date)).padStart(7, '0')}`;
		let t = at(date, series === 1 ? 7 * 60 : 14 * 60 + 30);
		return Array.from({ length: 89 }, (_, k) => {
			const i = k + 1;
			const rr = Math.round((0.92 + 0.06 * Math.sin(i / 4) + 0.01 * (i % 3) + 0.02 * (series - 1)) * 1000) / 1000;
			t += rr * 1000;
			return measurement(`rr-${series}-${date}-${i}`, 'rr_interval', 'sample', t, null, date, rr, 's', { external_id: `${uuid}#${i}` });
		});
	});
}

/** The day's activity summary as daily values with Apple's goals in context. */
function summary(date: string, codes: string[]) {
	const back = dayNumber(today) - dayNumber(date);
	if (back < 0 || back >= summaryDays) return [];
	const n = dayNumber(date);
	const paused = date === pausedDay;
	const part = back === 0 ? 0.55 : 1;
	const plan = [
		{ code: 'active_energy', unit: 'kcal', value: Math.round((420 + 140 * Math.sin(n / 3)) * part), goal: 500 },
		{ code: 'exercise_time', unit: 's', value: Math.round((26 + 14 * Math.sin(n / 2)) * part) * 60, goal: 1800 },
		{ code: 'stand_hours', unit: 'count', value: Math.round((9 + 3 * Math.sin(n / 2.5)) * part), goal: 12 }
	];
	return plan
		.filter((p) => codes.includes(p.code))
		.map((p) =>
			measurement(`sum-${p.code}-${date}`, p.code, 'daily_value', at(date, 0), at(addDay(date, 1), 0), date, paused ? 0 : p.value, p.unit,
				{ device: null, device_type: null, origin: 'vitamux.activity-summary', external_id: `summary-${date}` },
				{ goal: p.goal, move_mode: 'active_energy', paused })
		);
}

// ---- workouts -------------------------------------------------------------------------
const workoutPlans: Record<string, { provider: string; sport: string; date: string; from: number; to: number }> = {
	'w-run-apple': { provider: 'apple_health', sport: 'running', date: today, from: 7 * 60 + 11, to: 7 * 60 + 51 },
	'w-ride': { provider: 'garmin', sport: 'cycling', date: today, from: 18 * 60, to: 18 * 60 + 45 },
	'w-walk': { provider: 'garmin', sport: 'walking', date: '2026-09-10', from: 6 * 60 + 30, to: 7 * 60 + 15 }
};

function workout(id: string): Json | null {
	const p = workoutPlans[id];
	if (!p) return null;
	const minute = (m: number) => iso(at(p.date, p.from + m));
	let segments: Json[] = [];
	if (id === 'w-run-apple') {
		segments = [
			...Array.from({ length: 5 }, (_, lap) => ({ kind: 'lap', start_at: minute(lap * 8), end_at: minute(lap * 8 + 8), data: {} })),
			{ kind: 'pause', start_at: minute(19), end_at: minute(20), data: { type: 'pause' } },
			{ kind: 'marker', start_at: minute(30), end_at: null, data: { type: 'marker' } }
		];
	} else if (id === 'w-ride') {
		segments = [
			{ kind: 'activity', start_at: minute(0), end_at: minute(30), data: { sport: 'cycling', duration_s: 1800, location: 'outdoor', totals: { distance_m: 11200, energy_kcal: 290 } } },
			{ kind: 'activity', start_at: minute(32), end_at: minute(45), data: { sport: 'running', duration_s: 780, location: 'outdoor', totals: { distance_m: 2400, energy_kcal: 150 } } }
		];
	}
	segments.sort((a, b) => String(a.start_at).localeCompare(String(b.start_at)));
	return {
		id, start_at: minute(0), end_at: iso(at(p.date, p.to)), tz_offset_min: offset, local_date: p.date, sport: p.sport, provider_sport: p.sport,
		distance_m: 8000, energy_kcal: 395, avg_hr_bpm: 147, max_hr_bpm: 170, file_sha256: null,
		segments: segments.map((s, seq) => ({ seq, ...s })),
		source: source({ provider: p.provider, connection_id: `conn_${p.provider}` }), provenance
	};
}

/** Locations in the route (40 min at 1 Hz); the last ten are invalid fixes. */
export const routeCount = 2400;
export const routeInvalid = 10;

/** vitamux.route/1 of the Apple Health run: a loop at sea near 0°, 0°. */
function route(id: string): Json | null {
	const p = workoutPlans[id];
	if (id !== 'w-run-apple' || !p) return null;
	const turn = (i: number) => (2 * Math.PI * i) / routeCount;
	const all = Array.from({ length: routeCount }, (_, i) => i);
	return {
		format: 'vitamux.route/1', start: iso(at(p.date, p.from)), count: routeCount,
		offsets_s: all,
		latitude: all.map((i) => 0.01 + 0.004 * Math.sin(turn(i))),
		longitude: all.map((i) => 0.01 + 0.006 * (1 - Math.cos(turn(i))) + 0.0004 * Math.sin(5 * turn(i))),
		altitude_m: all.map(() => 0),
		horizontal_accuracy_m: all.map((i) => (i >= routeCount - routeInvalid ? -1 : 5)),
		speed_mps: all.map((i) => Math.round((3.2 + 0.3 * Math.sin(i / 60)) * 100) / 100)
	};
}

// ---- catalogue and inventory ----------------------------------------------------------
const catalogue: Record<string, Json> = {
	rr_interval: { code: 'rr_interval', section: 'Heart and circulation', unit: 's', kinds: ['sample'], aggregation: 'intensive', windows: [], strategies: [], plausible_range: [0.2, 3], provider_scoped: false, selection_only: false, unresolved: true, intraday: { default: '5m', finest: 'raw' } },
	stand_hours: { code: 'stand_hours', section: 'Activity', unit: 'count', kinds: ['interval', 'daily_value'], aggregation: 'additive', windows: ['bucket', 'hour', 'local_day'], strategies: ['single_source', 'first_available', 'latest'], plausible_range: [0, 24], provider_scoped: false, selection_only: false, intraday: { default: '30m', finest: '1m' } },
	hrv_rmssd: { code: 'hrv_rmssd', section: 'Heart and circulation', unit: 'ms', kinds: ['sample'], aggregation: 'intensive', windows: ['local_day', 'latest'], strategies: ['first_available'], plausible_range: [1, 500], provider_scoped: false, selection_only: false, intraday: { default: '5m', finest: 'raw' } }
};

const watchDevice = { id: 'dev_watch', type: 'watch', model: 'Synthetic Watch' };
const item = (kind: string, code: string, days: number, extra: Json): Json => ({
	kind, code, count: days * 3, days, first_date: '2026-07-17', last_date: today, first_at: '2026-07-17T06:00:00Z', last_at: '2026-09-14T06:05:00Z',
	providers: ['apple_health'], devices: [watchDevice], origins: [], ...extra
});

const inventory = [
	item('metric', 'rr_interval', 29, { metric: catalogue.rr_interval, latest: { local_date: today, value: 0.94, unit: 's' } }),
	item('metric', 'stand_hours', summaryDays, { metric: catalogue.stand_hours, latest: { local_date: today, value: 5, unit: 'count' } }),
	...Object.entries(Object.groupBy([...baseEvents, ...watchEvents], (e) => e.code)).map(([code, list = []]) =>
		item('event', code, list.length, { count: list.length, last_date: list.at(-1)?.local_date, latest: { local_date: list.at(-1)?.local_date ?? today, level: list.at(-1)?.level ?? undefined } })
	)
];

// ---- routes ---------------------------------------------------------------------------
export class WatchApi {
	/** The empty scenario: no Watch records at all. */
	empty = false;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const q = url.searchParams;
		const inRange = (date: string) => date >= (q.get('start_date') ?? '0000') && date <= (q.get('end_date') ?? '9999');
		let m: RegExpMatchArray | null;
		switch (path) {
			case '/inventory':
				return json(r, 200, { aggregates_pending: false, items: this.empty ? [] : inventory });
			case '/events': {
				const codes = q.getAll('code');
				const all = [...baseEvents, ...(this.empty ? [] : watchEvents)].toSorted((a, b) => a.start_at.localeCompare(b.start_at));
				return json(r, 200, { events: all.filter((e) => inRange(e.local_date) && (!codes.length || codes.includes(e.code))), has_more: false });
			}
			case '/measurements': {
				const metrics = q.getAll('metric');
				const start = q.get('start_date') ?? addDay(today, -summaryDays);
				const end = q.get('end_date') ?? today;
				const rows: Json[] = [];
				for (let d = start; d <= end && !this.empty; d = addDay(d, 1)) {
					if (metrics.includes('rr_interval')) rows.push(...beats(d));
					rows.push(...summary(d, metrics));
				}
				return json(r, 200, { measurements: rows, has_more: false });
			}
		}
		if ((m = path.match(/^\/metrics\/([^/]+)$/)) && catalogue[m[1]]) return json(r, 200, catalogue[m[1]]);
		if ((m = path.match(/^\/events\/([^/]+)\/waveform$/))) {
			const doc = this.empty ? null : waveform(m[1]);
			return doc ? json(r, 200, doc) : notFound(r, 'no waveform for this event');
		}
		if ((m = path.match(/^\/workouts\/([^/]+)\/route$/))) {
			const doc = route(m[1]);
			return doc ? json(r, 200, doc) : notFound(r, 'no route for this workout');
		}
		if ((m = path.match(/^\/workouts\/([^/]+)$/))) {
			const doc = workout(m[1]);
			return doc ? json(r, 200, doc) : notFound(r, 'no workout with this id');
		}
		return r.fallback();
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

function notFound(r: Route, detail: string) {
	return r.fulfill({
		status: 404,
		contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: 'not_found', status: 404, code: 'not_found', detail, request_id: 'req-e2e' })
	});
}

/** `test` with the views fakes plus a fresh WatchApi per test. */
export const test = viewsTest.extend<{ watch: WatchApi }>({
	watch: [
		async ({ page, views }, use) => {
			void views;
			const watch = new WatchApi(page);
			await watch.install();
			await use(watch);
		},
		{ auto: true }
	]
});

export { expect };
