// The Day view's resolution ladder (docs/architecture/resolution.md#windows, J26.3): the bucket
// for the visible span, never finer than a source's native spacing, and the loads behind one
// zoom level (GET /sources/series, then GET /resolved/series at the bucket the sources allow).
import { api, type Problem, type Schemas } from '../api/client.ts';
import { readAll } from '../data/paging.ts';

export type Bucket = '30s' | '1m' | '5m' | '15m' | '30m';
export type Step = Bucket | 'raw';
type Source = Schemas['SourceSeriesSource'];

const MIN = 60_000;
const HOUR = 60 * MIN;
const buckets: Bucket[] = ['30s', '1m', '5m', '15m', '30m'];
export const stepSeconds: Record<Step, number> = { '30s': 30, '1m': 60, '5m': 300, '15m': 900, '30m': 1800, raw: 0 };

/** Per catalogue default: each step with the widest span it is used for. */
const ladders: Record<Schemas['Intraday']['default'], [Step, number][]> = {
	'1m': [['1m', Infinity], ['30s', HOUR], ['raw', 15 * MIN]],
	'5m': [['5m', Infinity], ['1m', HOUR], ['raw', 15 * MIN]],
	'30m': [['30m', Infinity], ['15m', 6 * HOUR], ['5m', HOUR], ['1m', 15 * MIN]]
};

/** The metric's ladder from its 24-hour bucket down to its finest step. */
export function ladder(intraday: Schemas['Intraday']): [Step, number][] {
	const all = ladders[intraday.default];
	const end = all.findIndex(([s]) => s === intraday.finest);
	return end < 0 ? all : all.slice(0, end + 1);
}

/** The step for a visible span (ms): the finest one whose span covers it. */
export function stepFor(steps: [Step, number][], span: number): Step {
	return steps.reduce((pick, [s, max]) => (span <= max ? s : pick), steps[0][0]);
}

/** The step a source is drawn at: the requested one, or the finest bucket not finer than its spacing (rows as sent beyond 30 min). */
export function sourceStep(requested: Step, spacing?: number): Step {
	if (requested === 'raw' || spacing == null) return requested;
	const fit = buckets.find((b) => stepSeconds[b] >= spacing * 0.9);
	if (!fit) return 'raw';
	return stepSeconds[fit] > stepSeconds[requested] ? fit : requested;
}

/** The resolved bucket for a step: raw resolves at the finest bucket; never finer than the densest used source. */
export function resolvedBucket(steps: [Step, number][], step: Step, spacings: number[]): Bucket {
	const finest = [...steps].reverse().find(([s]) => s !== 'raw')?.[0] as Bucket;
	const b = step === 'raw' ? finest : step;
	if (!spacings.length) return b;
	const s = sourceStep(b, Math.min(...spacings));
	return s === 'raw' ? '30m' : s;
}

/** "1-minute buckets", "raw readings". */
export function stepWord(s: Step): string {
	if (s === 'raw') return 'raw readings';
	return s === '30s' ? '30-second buckets' : `${s.slice(0, -1)}-minute buckets`;
}

export interface DaySource {
	source: Source;
	step: Step;
	points: Schemas['SourcePoint'][];
}

/** One zoom level: the resolved points and each source's points over [from, to). */
export interface Layer {
	step: Step;
	bucket: Bucket;
	from: number;
	to: number;
	points: Schemas['ResolvedPoint'][];
	sources: DaySource[];
	problem: Problem | null;
}

const iso = (t: number) => new Date(t).toISOString();
const key = (s: Source) => [s.provider, s.connection_id, s.device?.id, s.origin?.key].join('|');

/** Every source's points at one grain, following raw pages. */
async function readSources(metric: string, grain: Step, from: number, to: number) {
	const out = new Map<string, Source>();
	let cursor: string | undefined;
	for (let i = 0; i < 50; i++) {
		const res = await api.GET('/api/v1/sources/series', { params: { query: { metric, start: iso(from), end: iso(to), grain, cursor } } });
		if (!res.data) return { sources: [...out.values()], problem: res.error ?? null };
		for (const s of res.data.sources) {
			const had = out.get(key(s));
			if (had) had.points.push(...s.points);
			else out.set(key(s), { ...s, points: [...s.points] });
		}
		if (!res.data.has_more || !res.data.next_cursor) break;
		cursor = res.data.next_cursor;
	}
	return { sources: [...out.values()], problem: null };
}

export async function loadLayer(metric: string, steps: [Step, number][], step: Step, from: number, to: number): Promise<Layer> {
	const first = await readSources(metric, step, from, to);
	let problem = first.problem;
	const sources: DaySource[] = first.sources.map((s) => ({ source: s, step: sourceStep(step, s.spacing_s), points: s.points }));
	// Sources sparser than the step are read again at their own.
	for (const grain of new Set(sources.map((s) => s.step).filter((g) => g !== step))) {
		const again = await readSources(metric, grain, from, to);
		problem ??= again.problem;
		const byKey = new Map(again.sources.map((s) => [key(s), s]));
		for (const s of sources) if (s.step === grain) s.points = byKey.get(key(s.source))?.points ?? [];
	}
	const spacings = first.sources.flatMap((s) => (s.rule_status === 'used' && s.spacing_s ? [s.spacing_s] : []));
	const bucket = resolvedBucket(steps, step, spacings);
	const resolved = await readAll(async (cursor) => {
		const res = await api.GET('/api/v1/resolved/series', { params: { query: { metric, start: iso(from), end: iso(to), window: bucket, limit: 3000, cursor } } });
		if (!res.data) return { items: [], problem: res.error };
		return { items: res.data.points, next: res.data.has_more ? res.data.next_cursor : undefined };
	});
	return { step, bucket, from, to, points: resolved.items, sources, problem: problem ?? resolved.problem ?? null };
}

/** The span to load for a zoomed view: aligned to the step's buckets from `start`, a bucket of margin, inside the day. */
export function loadSpan(step: Step, [lo, hi]: [number, number], [start, end]: [number, number]): [number, number] {
	const ms = Math.max(stepSeconds[step], 60) * 1000;
	const from = start + Math.floor((lo - start) / ms) * ms - ms;
	const to = start + Math.ceil((hi - start) / ms) * ms + ms;
	return [Math.max(from, start), Math.min(to, end)];
}
