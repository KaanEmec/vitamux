// What the Apple Watch views share (J22.18, docs/adr/0024-watch-data.md): an event's or a
// value's `context`, the event families, Apple's own words, and the shapes the views draw from
// (activity-summary days, heartbeat series, a route as a plain path). Copy rule: a value is shown
// as recorded, with its source. An ECG classification is HealthKit's label, never rephrased;
// nothing rates an ECG, rhythm, cycle or mood value, and no mark is coloured by its result.
// The iOS app has the same rules (apple/VitamuxApp/Sources/Features/Specialised/Watch).
import type { Schemas } from '../api/client.ts';
import { metricLabel } from '../data/format.ts';
import { recordLabel } from '../views/format.ts';

type Measurement = Schemas['Measurement'];

/** A `context` or segment `data` object as a plain record (the client types it as opaque). */
export const ctx = (o: unknown): Record<string, unknown> => (o && typeof o === 'object' ? (o as Record<string, unknown>) : {});
export const num = (o: unknown, key: string): number | null => {
	const v = ctx(o)[key];
	return typeof v === 'number' && Number.isFinite(v) ? v : null;
};
export const text = (o: unknown, key: string): string | null => {
	const v = ctx(o)[key];
	return typeof v === 'string' ? v : null;
};

/** An IANA zone for a whole-hour UTC offset (Etc/GMT signs are inverted), else undefined. */
export function offsetZone(min: number | null | undefined): string | undefined {
	if (min == null || min % 60 !== 0) return undefined;
	if (min === 0) return 'UTC';
	return `Etc/GMT${min > 0 ? '-' : '+'}${Math.abs(min) / 60}`;
}

// ---- event families ------------------------------------------------------------------------

/** The families the Events lanes and their type picker group codes by, in order. */
export const families = ['heart', 'mind', 'cycle', 'symptoms', 'alerts', 'other'] as const;
export type Family = (typeof families)[number];

export const familyTitle: Record<Family, string> = {
	heart: 'Heart rhythm and rate',
	mind: 'Mind',
	cycle: 'Cycle tracking',
	symptoms: 'Symptoms',
	alerts: 'Other alerts',
	other: 'Other events'
};

const heartCodes = new Set([
	'ecg_recording',
	'irregular_rhythm_alert',
	'irregular_rhythm',
	'afib_ecg_result',
	'afib_ppg_result',
	'high_heart_rate_alert',
	'low_heart_rate_alert',
	'low_cardio_fitness_alert',
	'hypertension_alert'
]);
const cycleCodes = new Set([
	'menstrual_flow',
	'bleeding_after_pregnancy',
	'bleeding_during_pregnancy',
	'bleeding_after_menopause',
	'intermenstrual_bleeding',
	'sexual_activity',
	'pregnancy',
	'lactation',
	'cervical_mucus',
	'ovulation_test',
	'pregnancy_test',
	'progesterone_test',
	'contraceptive',
	'menopausal_state',
	'irregular_cycles_alert',
	'infrequent_cycles_alert',
	'prolonged_periods_alert',
	'persistent_intermenstrual_bleeding_alert'
]);

export function familyOf(code: string): Family {
	if (heartCodes.has(code)) return 'heart';
	if (code === 'state_of_mind' || code === 'mindful_session') return 'mind';
	if (cycleCodes.has(code)) return 'cycle';
	if (code.startsWith('symptom_')) return 'symptoms';
	if (code.endsWith('_alert')) return 'alerts';
	return 'other';
}

/** Codes grouped by family in family order, each family's codes by name. */
export function byFamily<T extends { code: string }>(items: T[]): { family: Family; items: T[] }[] {
	return families
		.map((family) => ({ family, items: items.filter((i) => familyOf(i.code) === family).sort((a, b) => metricLabel(a.code).localeCompare(metricLabel(b.code))) }))
		.filter((g) => g.items.length);
}

// ---- Apple's words -------------------------------------------------------------------------

const classifications: Record<string, string> = {
	sinus_rhythm: 'Sinus rhythm',
	atrial_fibrillation: 'Atrial fibrillation',
	inconclusive_low_heart_rate: 'Inconclusive: low heart rate',
	inconclusive_high_heart_rate: 'Inconclusive: high heart rate',
	inconclusive_poor_reading: 'Inconclusive: poor reading',
	inconclusive_other: 'Inconclusive: other',
	not_set: 'Not set',
	unrecognized: 'Unrecognized'
};

/** HealthKit's `HKElectrocardiogram.Classification`, word for word: what the ECG app recorded. */
export const classification = (level: string | null | undefined) => (level ? (classifications[level] ?? metricLabel(level)) : 'No classification recorded');

/** Every classification phrase as the views print it (the copy review leaves Apple's words alone). */
export const classificationWords = Object.values(classifications);

/** HealthKit's `HKElectrocardiogram.SymptomsStatus`, as recorded. */
export function symptoms(status: string | null): string {
	if (status === 'present') return 'Recorded as present';
	if (status === 'none') return 'None recorded';
	return 'Not set';
}

export const lead = (l: string | null) => (l === 'apple_watch_similar_to_lead_i' ? 'Apple Watch, similar to lead I' : l ? metricLabel(l) : '–');

// ---- activity rings ------------------------------------------------------------------------

export const ringCodes = ['active_energy', 'move_time', 'exercise_time', 'stand_hours'];

export interface Ring {
	kind: 'move' | 'exercise' | 'stand';
	title: string;
	value: number;
	/** Apple's goal that day, when recorded. */
	goal: number | null;
	text: string;
}

export interface RingDay {
	date: string;
	rings: Ring[];
	paused: boolean;
	source: string;
}

const minutes = (s: number) => `${Math.round(s / 60)} min`;

function ring(kind: Ring['kind'], row: Measurement | undefined, unit: 'kcal' | 's' | 'hours'): Ring | null {
	if (!row) return null;
	const goal = num(row.context, 'goal');
	const fmt = (v: number) => (unit === 's' ? minutes(v) : unit === 'kcal' ? `${Math.round(v)} kcal` : `${Math.round(v)}`);
	const value = row.value;
	const body = unit === 'hours' ? `${fmt(value)}${goal == null ? '' : ` of ${fmt(goal)}`} hours` : `${fmt(value)}${goal == null ? '' : ` of ${fmt(goal)}`}`;
	return { kind, title: metricLabel(kind), value, goal, text: body };
}

/** The summary's fixed origin marker (ADR-0024: HealthKit reports no source) reads as words. */
const summarySource = (s: Schemas['SourceRef']) => (s.origin === 'vitamux.activity-summary' ? `${recordLabel({ ...s, origin: null, device_type: null })} · Activity summary` : recordLabel(s));

/** Apple's activity summaries (daily values with a goal or move mode in context) by day, newest first. */
export function ringDays(rows: Measurement[]): RingDay[] {
	const summaries = rows.filter((r) => num(r.context, 'goal') != null || text(r.context, 'move_mode') != null);
	const days = Object.entries(Object.groupBy(summaries, (r) => r.local_date));
	return days
		.map(([date, list = []]): RingDay => {
			const of = (code: string) => list.find((r) => r.metric === code);
			const moveTime = list.some((r) => text(r.context, 'move_mode') === 'move_time');
			const rings = [
				moveTime ? ring('move', of('move_time'), 's') : ring('move', of('active_energy'), 'kcal'),
				ring('exercise', of('exercise_time'), 's'),
				ring('stand', of('stand_hours'), 'hours')
			].filter((r) => r != null);
			return { date, rings, paused: list.some((r) => ctx(r.context).paused === true), source: summarySource(list[0].source) };
		})
		.sort((a, b) => b.date.localeCompare(a.date));
}

// ---- beat-to-beat --------------------------------------------------------------------------

/** Codes whose days open the beat-to-beat view: HRV readings and the RR series itself. */
export const beatCode = (code: string) => code.startsWith('hrv_') || code === 'rr_interval';

export interface BeatSeries {
	id: string;
	start: number;
	end: number;
	offset: number | null;
	source: string;
	xs: number[];
	/** Milliseconds. */
	ys: number[];
}

/** rr_interval rows grouped by the heartbeat series they came from (`<series uuid>#<beat>`), by start. */
export function beatSeries(rows: Measurement[]): BeatSeries[] {
	const groups = Object.groupBy(rows, (r) => r.source.external_id?.split('#')[0] ?? r.source.dedupe_key);
	return Object.entries(groups)
		.map(([id, list = []]) => {
			const sorted = list.toSorted((a, b) => a.start_at.localeCompare(b.start_at));
			const xs = sorted.map((r) => Date.parse(r.start_at));
			return {
				id,
				start: xs[0],
				end: xs[xs.length - 1],
				offset: sorted[0].tz_offset_min,
				source: recordLabel(sorted[0].source),
				xs,
				ys: sorted.map((r) => Math.round(r.unit === 's' ? r.value * 1000 : r.value))
			};
		})
		.sort((a, b) => a.start - b.start);
}

// ---- workout route -------------------------------------------------------------------------

export interface RoutePoint {
	/** Projected plane coordinates (x east, y south), in metres from the first point. */
	x: number;
	y: number;
	t: number;
	lat: number;
	lon: number;
	alt: number | null;
	speed: number | null;
}

/** At most this many points are drawn; the table lists the same points. */
export const routeMax = 1000;

/**
 * A route reduced for drawing: valid fixes only (CoreLocation marks an invalid one with a negative
 * horizontal accuracy), evenly strided to `routeMax`, first and last kept, projected on a local
 * equirectangular plane (no map, no tiles). Null with fewer than two valid fixes.
 */
export function routePoints(doc: Schemas['RouteDocument']): { points: RoutePoint[]; count: number } | null {
	const start = Date.parse(doc.start);
	const n = Math.min(doc.latitude.length, doc.longitude.length);
	const valid: number[] = [];
	for (let i = 0; i < n; i++) {
		const [lat, lon] = [doc.latitude[i], doc.longitude[i]];
		if (!(Math.abs(lat) <= 90 && Math.abs(lon) <= 180)) continue;
		if ((doc.horizontal_accuracy_m?.[i] ?? 0) < 0) continue;
		valid.push(i);
	}
	if (valid.length < 2) return null;
	const stride = Math.max(1, Math.ceil(valid.length / routeMax));
	const picked = valid.filter((_, k) => k % stride === 0);
	if (picked.at(-1) !== valid.at(-1)) picked.push(valid[valid.length - 1]);
	const lat0 = doc.latitude[picked[0]];
	const lon0 = doc.longitude[picked[0]];
	const m = 111_320; // metres per degree of latitude
	const k = Math.cos((lat0 * Math.PI) / 180);
	const valid0 = (a: number[] | undefined, i: number) => (a && a[i] != null && a[i] >= 0 ? a[i] : null);
	return {
		count: valid.length,
		points: picked.map((i) => ({
			x: (doc.longitude[i] - lon0) * m * k,
			y: -(doc.latitude[i] - lat0) * m,
			t: start + (doc.offsets_s[i] ?? 0) * 1000,
			lat: doc.latitude[i],
			lon: doc.longitude[i],
			alt: doc.altitude_m?.[i] ?? null,
			speed: valid0(doc.speed_mps, i)
		}))
	};
}

// ---- workout segments ----------------------------------------------------------------------

type Segment = Schemas['WorkoutSegment'];
const kinds: Segment['kind'][] = ['activity', 'lap', 'interval', 'set', 'pause', 'marker'];
const plural: Record<Segment['kind'], string> = { activity: 'Activities', lap: 'Laps', interval: 'Intervals', set: 'Sets', pause: 'Pauses', marker: 'Markers' };

/** The segment's place among those of its kind, from 1. */
const numberOf = (s: Segment, all: Segment[]) => all.filter((o) => o.kind === s.kind && o.seq <= s.seq).length;

export function segmentTitle(s: Segment, all: Segment[]): string {
	const n = numberOf(s, all);
	const type = text(s.data, 'type');
	switch (s.kind) {
		case 'activity':
			return text(s.data, 'sport') ? metricLabel(text(s.data, 'sport') ?? '') : `Activity ${n}`;
		case 'pause':
			return type === 'motion_pause' ? `Motion pause ${n}` : `Pause ${n}`;
		case 'marker':
			return type === 'pause_or_resume_request' ? 'Pause or resume request' : `Marker ${n}`;
		default:
			return `${metricLabel(s.kind)} ${n}`;
	}
}

/** Lanes of segments by kind, activities first. */
export function segmentLanes(all: Segment[]) {
	return kinds
		.map((kind) => ({
			label: plural[kind],
			events: all
				.filter((s) => s.kind === kind)
				.map((s) => {
					const start = Date.parse(s.start_at);
					return { start, end: s.end_at ? Date.parse(s.end_at) : start, label: segmentTitle(s, all) };
				})
		}))
		.filter((l) => l.events.length);
}
