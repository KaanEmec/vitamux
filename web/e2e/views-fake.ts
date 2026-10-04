// A stand-in for the endpoints behind the specialised views (J21.9: sleep, blood pressure, body
// composition, workouts, events, lab analytes), on top of fake-api.ts. All values are synthetic.
//
// The clock is fixed at 2026-09-14 (noon UTC), so "today" and the range presets are stable.
// Sleep: 30 nights ending 2026-09-14. Night 2026-09-14 has three sources (a selected watch with
// stages, a second source with stages, an excluded relayed one without); night 2026-09-13 has
// a nap. Every other night has one session from the watch.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;

export const lastNight = '2026-09-14';
export const napNight = '2026-09-13';
const offset = 120;
const nightCount = 30;

const addDay = (d: string, n: number) => new Date(Date.parse(`${d}T00:00:00Z`) + n * 86_400_000).toISOString().slice(0, 10);
/** UTC instant of a local wall-clock time (the fixtures all sit at +02:00). */
const at = (date: string, hh: number, mm = 0) => new Date(Date.parse(`${date}T00:00:00Z`) + (hh * 60 + mm - offset) * 60_000).toISOString();

const provenance = { raw_payload_id: '5', normalizer: 'synthetic@1', ingested_at: '2026-09-14T06:00:00Z', normalized_at: '2026-09-14T06:00:01Z', superseded_at: null, superseded_by: null, deleted_at: null, deleted_by_raw_id: null };
const source = (provider: string, over: Json = {}) => ({ provider, connection_id: `conn_${provider}`, device: null, device_type: null, origin: null, external_id: null, dedupe_key: '0'.repeat(32), ...over });

// ---- sleep ----------------------------------------------------------------------------
interface SleepFixture extends Json {
	id: string;
	sleep_date: string;
	is_nap: boolean;
	has_stages: boolean;
	start_at: string;
	end_at: string;
	stages: { stage: string; start_at: string; end_at: string }[];
}

/** Stage segments from `start` to `end`; `shift` minutes longer in the first one, so sources differ. */
const stages = (start: string, end: string, shift: number): SleepFixture['stages'] => {
	const plan: [string, number][] = [['light', 70 + shift], ['deep', 80], ['rem', 55], ['awake', 10], ['light', 120], ['rem', 50], ['light', 600]];
	const stop = Date.parse(end);
	let t = Date.parse(start);
	const out: SleepFixture['stages'] = [];
	for (const [stage, min] of plan) {
		if (t >= stop) break;
		const to = Math.min(stop, t + min * 60_000);
		out.push({ stage, start_at: new Date(t).toISOString(), end_at: new Date(to).toISOString() });
		t = to;
	}
	return out;
};

function session(id: string, sleepDate: string, provider: string, start: string, end: string, over: Json = {}): SleepFixture {
	const asleep = Math.round((Date.parse(end) - Date.parse(start)) / 1000 - 1500);
	return {
		id, start_at: start, end_at: end, tz_offset_min: offset, sleep_date: sleepDate, is_nap: false, has_stages: true, totals_basis: 'stages',
		asleep_s: asleep, deep_s: 5400, light_s: asleep - 5400 - 6000, rem_s: 6000, awake_s: 1500, latency_s: 600, stages: [],
		source: source(provider), provenance, ...over
	} as SleepFixture;
}

const dates = Array.from({ length: nightCount }, (_, i) => addDay(lastNight, i - nightCount + 1));
const sessions: SleepFixture[] = [];
const night = (date: string, i: number) => {
	// Bed 23:00-23:28, wake about 06:50-07:50: varied, so the consistency chart has something to show.
	const start = at(addDay(date, -1), 23, (i * 7) % 30);
	const end = at(date, 6, 50 + ((i * 13) % 60));
	const main = session(`sleep-${date}-watch`, date, 'apple_health', start, end, { source: source('apple_health', { device_type: 'watch' }) });
	main.stages = stages(start, end, 0);
	sessions.push(main);
	if (date >= napNight) {
		const other = session(`sleep-${date}-band`, date, 'whoop', at(addDay(date, -1), 23, 25), at(date, 7, 5), { source: source('whoop', { device_type: 'band' }) });
		other.stages = stages(other.start_at, other.end_at, 15);
		sessions.push(other);
	}
};
dates.forEach((d, i) => night(d, i));
// The excluded, relayed source on the last night: no stages.
sessions.push(
	session(`sleep-${lastNight}-relay`, lastNight, 'apple_health', at(addDay(lastNight, -1), 23, 5), at(lastNight, 6, 55), {
		has_stages: false, deep_s: null, light_s: null, rem_s: null, awake_s: null, source: source('apple_health', { origin: 'com.garmin.connect.mobile' })
	})
);
sessions.push(session(`nap-${napNight}`, napNight, 'apple_health', at(napNight, 14, 10), at(napNight, 14, 50), { is_nap: true, has_stages: false, source: source('apple_health', { device_type: 'watch' }) }));

const member = (id: string, provider: string, over: Json) => {
	const s = sessions.find((x) => x.id === id) as SleepFixture;
	const seconds = (Date.parse(s.end_at) - Date.parse(s.start_at)) / 1000;
	return { group: provider, rule_status: 'used', selected: false, provider, values: { sessions: 1, sleep_in_bed: seconds, sleep_total: s.asleep_s }, session_refs: [id], ...over };
};

/** The night with no episode in any source: a gap in every chart. */
export const gapNight = dates[9];

function resolvedNight(date: string): Json {
	if (date === gapNight) {
		return { local_date: date, result: { status: 'no_data', window: { kind: 'local_night', local_date: date }, explanation: 'No source recorded this night.' }, members: [] };
	}
	const main = sessions.find((s) => s.id === `sleep-${date}-watch`) as SleepFixture;
	const watch = member(main.id, 'apple_watch', { selected: true, provider: 'apple_health', device: { type: 'watch', model: 'Apple Watch' } });
	const members: Json[] = [watch];
	if (date >= napNight) members.push(member(`sleep-${date}-band`, 'whoop', { provider: 'whoop', device: { type: 'band' } }));
	if (date === lastNight) {
		members.push(member(`sleep-${date}-relay`, 'garmin', { group: null, rule_status: 'excluded', reason: 'exclude: relayed=true', provider: 'apple_health', origin: { key: 'com.garmin.connect.mobile', name: 'Garmin Connect', relayed: true, relayed_provider: 'garmin' } }));
	}
	const value = {
		sleep_total: main.asleep_s, sleep_in_bed: (watch.values as Json).sleep_in_bed, sleep_deep: main.deep_s, sleep_light: main.light_s,
		sleep_rem: main.rem_s, sleep_awake: main.awake_s, sleep_latency: main.latency_s
	};
	return {
		local_date: date,
		episode: { start: main.start_at, end: main.end_at },
		result: { status: 'direct', value, window: { kind: 'local_night', local_date: date }, rule: { ref: 'builtin:sleep:1', version: 1, strategy: 'event_priority' }, selected: 'apple_watch', explanation: 'Used apple_watch, the first source in the rule with an episode on this night.' },
		members
	};
}

// ---- blood pressure -------------------------------------------------------------------
const readings = Array.from({ length: 20 }, (_, i) => {
	const date = addDay(lastNight, i - 19);
	const manual = i === 18;
	return {
		id: `bp-${date}`, measured_at: at(date, 7, 10), tz_offset_min: offset, local_date: date,
		systolic: 118 + (i % 7), diastolic: 74 + (i % 5), pulse: 58 + (i % 6), context: { position: 'seated', arm: 'left' },
		source: manual ? source('manual') : source('withings', { device_type: 'bp_monitor' }), provenance
	};
});

// A second reading ten minutes after the newest (one session) and an evening reading.
readings.push(
	{ ...readings[19], id: `bp-${lastNight}-b`, measured_at: at(lastNight, 7, 20), systolic: 124, diastolic: 80, pulse: 61 },
	{ ...readings[15], id: `bp-${addDay(lastNight, -4)}-eve`, measured_at: at(addDay(lastNight, -4), 19, 30), systolic: 126, diastolic: 82, pulse: 66 }
);
readings.sort((a, b) => a.measured_at.localeCompare(b.measured_at));

// ---- body composition -----------------------------------------------------------------
const comp = (id: string, metric: string, value: number, unit = 'kg') => ({ id: `${id}-${metric}`, metric, value, unit, source_value: null, source_unit: null, quality_flags: 0 });
const weighIns = Array.from({ length: 10 }, (_, i) => {
	const date = addDay(lastNight, (i - 9) * 3);
	const id = `body-${date}`;
	const weight = 80 - i * 0.2;
	const fat = 16 - i * 0.1;
	return {
		id, kind: 'body_composition', measured_at: at(date, 6, 40), tz_offset_min: offset, local_date: date, context: {}, provenance,
		components: [comp(id, 'weight', weight), comp(id, 'fat_mass', fat), comp(id, 'fat_free_mass', weight - fat), comp(id, 'muscle_mass', 36 - i * 0.05), comp(id, 'bone_mass', 3.1)],
		source: source('withings', { device_type: 'scale' })
	};
});
weighIns.push({
	id: 'body-apple', kind: 'body_composition', measured_at: at(addDay(lastNight, -1), 8, 0), tz_offset_min: offset, local_date: addDay(lastNight, -1), context: {}, provenance,
	components: [comp('body-apple', 'weight', 78.9)], source: source('apple_health')
});
weighIns.sort((a, b) => a.measured_at.localeCompare(b.measured_at));

// ---- workouts -------------------------------------------------------------------------
const wm = (id: string, provider: string, start: string, end: string, sport: string, over: Json = {}) => ({
	id, group: provider, rule_status: 'used', selected: false, start_at: start, end_at: end, sport, distance_m: 8100, energy_kcal: 410, avg_hr_bpm: 148, max_hr_bpm: 171, provider, ...over
});
const workoutClusters = [
	{
		local_date: '2026-09-14', start: at('2026-09-14', 7, 10), end: at('2026-09-14', 7, 52), sport: 'running', selected: 'w-run-garmin', group: 'garmin',
		explanation: 'Used garmin: the first source in the rule for this workout.',
		members: [wm('w-run-garmin', 'garmin', at('2026-09-14', 7, 10), at('2026-09-14', 7, 52), 'running', { selected: true, device: { type: 'watch', model: 'Forerunner' } }), wm('w-run-apple', 'apple_health', at('2026-09-14', 7, 11), at('2026-09-14', 7, 51), 'running', { distance_m: 8000, group: null, rule_status: 'not_in_rule' })]
	},
	{
		local_date: '2026-09-14', start: at('2026-09-14', 18, 0), end: at('2026-09-14', 18, 45), sport: 'cycling', selected: 'w-ride', group: 'garmin', explanation: 'Only one source recorded this workout.',
		members: [wm('w-ride', 'garmin', at('2026-09-14', 18, 0), at('2026-09-14', 18, 45), 'cycling', { selected: true, distance_m: 15000, avg_hr_bpm: 132 })]
	},
	{
		local_date: '2026-09-10', start: at('2026-09-10', 6, 30), end: at('2026-09-10', 7, 15), sport: 'walking', selected: 'w-walk', group: 'garmin', explanation: 'Only one source recorded this workout.',
		members: [wm('w-walk', 'garmin', at('2026-09-10', 6, 30), at('2026-09-10', 7, 15), 'walking', { selected: true, distance_m: 3800, avg_hr_bpm: 101 })]
	}
];

// ---- events ---------------------------------------------------------------------------
const event = (id: string, code: string, date: string, hh: number, level: string, endMin?: number) => ({
	id, code, start_at: at(date, hh, 20), end_at: endMin ? at(date, hh, 20 + endMin) : null, tz_offset_min: offset, local_date: date, value: null, level,
	context: {}, quality_flags: 0, source: source('apple_health'), provenance
});
const events = [
	event('ev1', 'environment_audio_alert', '2026-09-02', 17, 'momentary_limit'),
	event('ev2', 'environment_audio_alert', '2026-09-09', 12, 'momentary_limit', 30),
	event('ev3', 'headphone_audio_alert', '2026-09-11', 20, 'seven_day_limit')
];

// ---- lab ------------------------------------------------------------------------------
const result = (id: string, date: string, over: Json) => ({
	id, revision: 1, analyte: 'glucose', original_label: 'Glucose', value_text: '5.1', value_numeric: 5.1, comparator: null, unit_text: 'mmol/L',
	reference_range_text: '3.9 - 5.5', ref_low: 3.9, ref_high: 5.5, printed_flag: null, specimen_type: 'Serum', canonical_value: null, canonical_unit: null,
	conversion_factor: null, conversion_offset: null, catalog_version: 1, collected_at: date, collected_date: date, page: 1, evidence_text: null,
	created_at: '2026-09-01T00:00:00Z', updated_at: '2026-09-01T00:00:00Z',
	provenance: { report_id: `rep_${id}`, document_id: `doc_${id}`, extraction_id: null, row_index: 0, laboratory: 'Synthetic Lab', reported_at: date, provider: 'fake', model: null, schema_version: '1', prompt_version: '1', confirmed_by: 'owner', confirmed_at: '2026-09-01T00:00:00Z' },
	...over
});
export const labResults = [
	result('lab_a1', '2025-03-10', { value_text: '4.9', value_numeric: 4.9 }),
	result('lab_a2', '2025-09-12', { value_text: '5.3', value_numeric: 5.3 }),
	result('lab_a3', '2026-03-05', { value_text: '< 0.5', value_numeric: 0.5, comparator: '<', printed_flag: 'L' }),
	result('lab_a4', '2026-08-30', { value_text: '95', value_numeric: 95, unit_text: 'mg/dL', reference_range_text: '70 - 99', ref_low: 70, ref_high: 99 }),
	result('lab_b1', '2026-08-30', { analyte: 'creatinine', original_label: 'Creatinine', value_text: '88', value_numeric: 88, unit_text: 'umol/L', reference_range_text: '60 - 110', ref_low: 60, ref_high: 110 })
];

// ---- routes ---------------------------------------------------------------------------
export class ViewsApi {
	/** Query strings of GET /events, in order. */
	eventQueries: string[] = [];

	constructor(private page: Page) {}

	async install() {
		await this.page.clock.setFixedTime(new Date('2026-09-14T12:00:00Z'));
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const q = url.searchParams;
		const inRange = (date: string) => date >= (q.get('start_date') ?? '0000') && date <= (q.get('end_date') ?? '9999');
		switch (path) {
			case '/resolved/sleep':
				return json(r, 200, { timezone: 'Europe/Amsterdam', nights: dates.filter(inRange).map(resolvedNight) });
			case '/sleep': {
				const withStages = q.get('include')?.includes('stages');
				return json(r, 200, { sleep: sessions.filter((s) => inRange(s.sleep_date)).map((s) => (withStages ? s : { ...s, stages: undefined })), has_more: false });
			}
			case '/blood-pressure':
				return json(r, 200, { readings: readings.filter((x) => inRange(x.local_date)), has_more: false });
			case '/groups':
				return json(r, 200, { groups: weighIns.filter((g) => inRange(g.local_date)), has_more: false });
			case '/resolved/workouts':
				return json(r, 200, { timezone: 'Europe/Amsterdam', rule: { ref: 'builtin:heart_rate:1', version: 1 }, workouts: workoutClusters.filter((c) => inRange(c.local_date)) });
			case '/inventory':
				return json(r, 200, {
					aggregates_pending: false,
					items: [
						{ kind: 'event', code: 'environment_audio_alert', count: 2, days: 2, first_date: '2026-09-02', last_date: '2026-09-09', providers: ['apple_health'], devices: [], origins: [] },
						{ kind: 'event', code: 'headphone_audio_alert', count: 1, days: 1, first_date: '2026-09-11', last_date: '2026-09-11', providers: ['apple_health'], devices: [], origins: [] }
					]
				});
			case '/events': {
				this.eventQueries.push(url.search);
				const codes = q.getAll('code');
				return json(r, 200, { events: events.filter((e) => inRange(e.local_date) && (!codes.length || codes.includes(e.code))), has_more: false });
			}
			case '/lab-results':
				return json(r, 200, { lab_results: labResults, has_more: false });
		}
		const m = path.match(/^\/provenance\/(\w+)\/([^/]+)$/);
		if (m) {
			const v = { id: '1', superseded_by: null, record: { id: m[2] }, provider: 'synthetic', connection_id: 'conn_synthetic', connection_mode: 'sync', client: null, batch: null, raw: null, normalizer: { name: 'synthetic', version: 1, git_sha: 'abcdef0123456789' }, fetched_at: '2026-09-14T06:00:00Z', ingested_at: '2026-09-14T06:00:00Z', normalized_at: '2026-09-14T06:00:01Z', corrected_at: null, superseded_at: null, deleted_at: null, deleted_by: null };
			return json(r, 200, { entity: m[1], row: v, earlier: [], later: [] });
		}
		return r.fallback();
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

/** `test` with the base FakeApi (signed in) plus a fresh ViewsApi per test. */
export const test = base.extend<{ views: ViewsApi }>({
	views: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const views = new ViewsApi(page);
			await views.install();
			await use(views);
		},
		{ auto: true }
	]
});

export { expect };
