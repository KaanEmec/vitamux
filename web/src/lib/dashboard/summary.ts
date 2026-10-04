// GET /resolved/summary into what a card shows: the value, a neutral delta against the 30-day
// mean, a sparkline, source chips and the status. Plain statistics only; nothing is judged.
import { api, type Problem, type Schemas } from '../api/client.ts';
import { metricLabel } from '../data/format.ts';
import { providerLabel } from '../connections/connections.ts';
import { stageLabels } from '../charts/sleep.ts';

export type Summary = Schemas['MetricSummary'];

export interface CardView {
	value: string;
	unit: string;
	/** A second line: the pulse of a blood pressure reading, the time in bed. */
	sub: string;
	delta: string;
	ys: (number | null)[];
	band?: [number, number];
	mean?: number;
	bars: boolean;
	status: string;
	partial: boolean;
	/** Anything stored for this metric in the last 90 days, or a value on the day. */
	hasData: boolean;
	chips: { provider?: string; label: string }[];
	/** Sleep: minutes per stage in display order, absent when the source has no stage data. */
	stages?: { stage: string; label: string; seconds: number }[];
}

const perRequest = 20; // the endpoint's cap

/** Summaries of `metrics` on `date` (the owner's today when omitted). */
export async function loadSummaries(metrics: string[], date?: string) {
	const chunks: string[][] = [];
	for (let i = 0; i < metrics.length; i += perRequest) chunks.push(metrics.slice(i, i + perRequest));
	const answers = await Promise.all(
		chunks.map((c) =>
			api.GET('/api/v1/resolved/summary', {
				params: { query: { metrics: c, date } },
				querySerializer: { array: { style: 'form', explode: false } } // metrics=a,b (spec: explode false)
			})
		)
	);
	const error: Problem | null = answers.find((a) => a.error)?.error ?? null;
	const data = answers.flatMap((a) => (a.data ? [a.data] : []));
	return {
		error,
		date: data[0]?.date,
		timezone: data[0]?.timezone ?? '',
		summaries: Object.assign({}, ...data.map((d) => d.metrics)) as Record<string, Summary>
	};
}

const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });
const whole = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });
const fmt = (n: number) => (Math.abs(n) >= 1000 ? whole : number).format(n);
const num = (v: unknown): number | null => (typeof v === 'number' ? v : null);
const part = (v: unknown, code: string): number | null => num(v && typeof v === 'object' ? (v as Record<string, unknown>)[code] : undefined);

/** "7h 19m" for seconds. */
export function hoursMinutes(seconds: number): string {
	const m = Math.round(seconds / 60);
	return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
}

const stages = [
	['deep', 'sleep_deep'],
	['light', 'sleep_light'],
	['rem', 'sleep_rem'],
	['asleep_unspecified', 'sleep_unspecified'],
	['awake', 'sleep_awake']
] as const;

/** The code whose daily values draw a family's sparkline. */
const lead: Record<string, string> = { sleep: 'sleep_total', blood_pressure: 'bp_systolic' };

export function cardView(code: string, s: Summary, additive: boolean): CardView {
	const v = s.value;
	const family = code in lead;
	const point = (x: unknown) => (family ? part(x, lead[code]) : num(x));
	const thirty = s.stats[1];
	const stat = (field: 'mean' | 'min' | 'max') => (family ? thirty?.components?.[lead[code]]?.[field] : thirty?.[field]);
	const mean = stat('mean');
	const ys = s.sparkline.map((p) => point(p.value));

	const seen = new Set<string>();
	const chips = (v.inputs ?? []).flatMap((i) => {
		if (!i.selected || !i.group || seen.has(i.group)) return [];
		seen.add(i.group);
		const provider = i.sources?.[0]?.provider;
		return [{ provider, label: provider === i.group ? providerLabel(provider) : metricLabel(i.group) }];
	});

	const out: CardView = {
		value: '–',
		unit: '',
		sub: '',
		delta: '',
		ys,
		bars: additive || code === 'sleep',
		status: v.status,
		partial: !!v.partial,
		hasData: v.status !== 'no_data' || s.sparkline.some((p) => p.status !== 'no_data') || s.stats.some((r) => r.n > 0),
		chips
	};
	if (mean != null && (thirty?.n ?? 0) > 1 && !out.bars) {
		const [lo, hi] = [stat('min'), stat('max')];
		if (lo != null && hi != null) out.band = [lo, hi];
	}
	if (mean != null) out.mean = mean;

	if (code === 'sleep') {
		const total = part(v.value, 'sleep_total');
		if (total != null) {
			out.value = hoursMinutes(total);
			out.unit = 'asleep';
			const inBed = part(v.value, 'sleep_in_bed');
			if (inBed != null) out.sub = `in bed ${hoursMinutes(inBed)}`;
			const parts = stages.map(([stage, c]) => ({ stage, label: stageLabels[stage], seconds: part(v.value, c) ?? 0 }));
			if (parts.some((p) => p.seconds > 0 && p.stage !== 'asleep_unspecified')) out.stages = parts.filter((p) => p.seconds > 0);
		}
		if (mean != null) out.delta = `30-day mean ${hoursMinutes(mean)}`;
	} else if (code === 'blood_pressure') {
		const [sys, dia, pulse] = [part(v.value, 'bp_systolic'), part(v.value, 'bp_diastolic'), part(v.value, 'bp_pulse')];
		if (sys != null && dia != null) {
			out.value = `${fmt(sys)}/${fmt(dia)}`;
			out.unit = 'mmHg';
			if (pulse != null) out.sub = `pulse ${fmt(pulse)} bpm`;
		}
		const dMean = part(thirty?.components?.bp_diastolic, 'mean');
		if (mean != null && dMean != null) out.delta = `30-day mean ${fmt(mean)}/${fmt(dMean)}`;
	} else {
		const n = num(v.value);
		if (n != null) {
			out.value = fmt(n);
			out.unit = v.unit === 'count' ? (v.partial ? 'so far' : '') : (v.unit ?? s.unit ?? '');
			if (mean != null) out.delta = additive ? `30-day mean ${fmt(mean)}` : `${sign(n - mean)} vs 30-day mean`;
		}
	}
	return out;
}

/** "+4", "−1", "±0": the delta rounded the way it is shown. */
function sign(d: number): string {
	const r = Math.round(d * 10) / 10;
	return `${r > 0 ? '+' : r < 0 ? '−' : '±'}${number.format(Math.abs(r))}`;
}
