// The typed rule of schemas/resolution-rule.v1.json, the builder's form model, and the
// helpers the rules pages share (docs/architecture/resolution.md#rule-specification).
// The server validates every rule; nothing here duplicates the catalogue checks.

export const selectorFields = [
	'provider',
	'device_type',
	'relayed',
	'origin_key',
	'origin_key_prefix',
	'origin_name',
	'device_model',
	'device_id',
	'connection_id',
	'entry'
] as const;
export type SelectorField = (typeof selectorFields)[number];
export type Selector = Partial<Record<SelectorField, string | boolean>>;

export const selectorLabels: Record<SelectorField, string> = {
	provider: 'Provider',
	device_type: 'Device type',
	relayed: 'Relayed',
	origin_key: 'Origin app id',
	origin_key_prefix: 'Origin app id starts with',
	origin_name: 'Origin app name',
	device_model: 'Device model',
	device_id: 'Device id',
	connection_id: 'Connection id',
	entry: 'Entry'
};

export type Op =
	| 'single_source'
	| 'first_available'
	| 'mean_across_sources'
	| 'minimum_across_sources'
	| 'maximum_across_sources'
	| 'sum_across_sources'
	| 'latest'
	| 'earliest'
	| 'event_priority';

export interface Rule {
	schema: 'vitamux.rule/1';
	metric: string;
	window: { kind: string; size?: string };
	groups: { id: string; match: Selector[] }[];
	exclude?: Selector[];
	within_source?: { intra_group?: string; daily_value_policy?: string; statistic?: string; span?: string };
	strategy: { op: Op; min_sources?: number; on_insufficient?: string };
	quality?: {
		min_coverage?: number;
		plausible_range?: [number, number];
		exclude_flags?: string[];
		max_staleness?: string;
		require_wear?: string;
		sleep?: { match_overlap?: number; min_episode_coverage?: number; include_naps?: boolean; night_anchor?: string };
	};
	contexts?: { workout?: string[]; sleep?: string[] };
	follow?: string;
	compose?: { from: 'hour'; op: string };
	acknowledged_warnings?: string[];
}

/** Plain-language strategy cards (frontend.md#rule-builder). */
export const ops: { op: Op; label: string; hint: string; pooling?: boolean }[] = [
	{ op: 'first_available', label: 'Use the first source with data', hint: 'Groups are tried in order, per window.' },
	{ op: 'single_source', label: 'Use one source only', hint: 'Exactly one group; no fallback.' },
	{ op: 'mean_across_sources', label: 'Average the sources', hint: 'Mean of the valid groups.', pooling: true },
	{ op: 'minimum_across_sources', label: 'Take the lowest', hint: 'The lowest valid group.', pooling: true },
	{ op: 'maximum_across_sources', label: 'Take the highest', hint: 'The highest valid group.', pooling: true },
	{
		op: 'sum_across_sources',
		label: 'Add the sources together',
		hint: 'Sum of the valid groups. The same activity can be counted twice.',
		pooling: true
	},
	{ op: 'latest', label: 'Use the newest value', hint: 'Ties go by group order.' },
	{ op: 'earliest', label: 'Use the oldest value', hint: 'Ties go by group order.' },
	{ op: 'event_priority', label: 'Use the whole event from the first source', hint: 'Sleep or workouts, never spliced.' }
];

export const opLabel = (op: string) => ops.find((o) => o.op === op)?.label ?? op;

export const windowKinds: { kind: string; label: string }[] = [
	{ kind: 'bucket', label: 'Fixed buckets (minutes)' },
	{ kind: 'hour', label: 'Local hour' },
	{ kind: 'local_day', label: 'Local day' },
	{ kind: 'local_night', label: 'Night (main sleep episode)' },
	{ kind: 'sleep_episode', label: 'Every sleep episode' },
	{ kind: 'latest', label: 'Latest value' },
	{ kind: 'reading', label: 'Each reading' }
];
export const bucketSizes = ['1m', '5m', '15m', '30m'];

export function windowLabel(w: Rule['window']): string {
	const k = windowKinds.find((x) => x.kind === w.kind)?.label ?? w.kind;
	return w.kind === 'bucket' && w.size ? `${w.size} buckets` : k;
}

export const qualityFlags = [
	'manual_entry',
	'motion_context',
	'implausible',
	'relayed',
	'migrated_without_raw',
	'prorated_source'
];

export const sumWarning = 'cross_source_sum_duplicate_risk';

/** The builder's form: strings for inputs, converted by toSpec. Unknown extensions pass through. */
export interface Form {
	metric: string;
	windowKind: string;
	bucketSize: string;
	groups: { key: number; id: string; match: Selector[] }[];
	exclude: Selector[];
	op: Op;
	minSources: string;
	onInsufficient: string;
	intraGroup: string;
	dailyValuePolicy: string;
	statistic: string;
	span: string;
	minCoverage: string;
	rangeLow: string;
	rangeHigh: string;
	excludeFlags: string[];
	maxStaleness: string;
	requireWear: string;
	matchOverlap: string;
	minEpisodeCoverage: string;
	includeNaps: string;
	nightAnchor: string;
	follow: string;
	compose: string;
	acknowledged: string[];
	contexts?: Rule['contexts'];
}

let nextKey = 1;
export const groupKey = () => nextKey++;

const str = (v: unknown) => (v === undefined || v === null ? '' : String(v));

export function blankForm(metric: string): Form {
	return fromSpec({
		schema: 'vitamux.rule/1',
		metric,
		window: { kind: 'local_day' },
		groups: [{ id: '', match: [{ provider: '' }] }],
		strategy: { op: 'first_available' }
	});
}

export function fromSpec(r: Rule): Form {
	const q = r.quality ?? {};
	const ws = r.within_source ?? {};
	return {
		metric: r.metric,
		windowKind: r.window.kind,
		bucketSize: r.window.size ?? '5m',
		groups: r.groups.map((g) => ({ key: groupKey(), id: g.id, match: g.match.map((s) => ({ ...s })) })),
		exclude: (r.exclude ?? []).map((s) => ({ ...s })),
		op: r.strategy.op,
		minSources: str(r.strategy.min_sources),
		onInsufficient: str(r.strategy.on_insufficient),
		intraGroup: str(ws.intra_group),
		dailyValuePolicy: str(ws.daily_value_policy),
		statistic: str(ws.statistic),
		span: str(ws.span),
		minCoverage: str(q.min_coverage),
		rangeLow: str(q.plausible_range?.[0]),
		rangeHigh: str(q.plausible_range?.[1]),
		excludeFlags: [...(q.exclude_flags ?? [])],
		maxStaleness: str(q.max_staleness),
		requireWear: str(q.require_wear),
		matchOverlap: str(q.sleep?.match_overlap),
		minEpisodeCoverage: str(q.sleep?.min_episode_coverage),
		includeNaps: str(q.sleep?.include_naps),
		nightAnchor: str(q.sleep?.night_anchor),
		follow: str(r.follow),
		compose: str(r.compose?.op),
		acknowledged: [...(r.acknowledged_warnings ?? [])],
		contexts: r.contexts ? JSON.parse(JSON.stringify(r.contexts)) : undefined // works on $state proxies, unlike structuredClone
	};
}

// A number when the text is one; otherwise the text, so the server names the bad field.
function num(s: string): number | string | undefined {
	const t = s.trim();
	if (t === '') return undefined;
	return Number.isFinite(Number(t)) ? Number(t) : t;
}

function compact<T extends object>(o: T): T | undefined {
	const out = Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== ''));
	return Object.keys(out).length ? (out as T) : undefined;
}

function cleanSelector(s: Selector): Selector {
	return Object.fromEntries(Object.entries(s).filter(([, v]) => v !== '')) as Selector;
}

/** True when the form sums across or within sources and so needs the duplicate-risk acknowledgement. */
export const needsSumAck = (f: Form) => f.op === 'sum_across_sources' || f.intraGroup === 'sum';

/** The typed rule JSON for the form. Empty inputs are left out; the server validates the rest. */
export function toSpec(f: Form): Rule {
	const pooling = ops.find((o) => o.op === f.op)?.pooling;
	const low = num(f.rangeLow);
	const high = num(f.rangeHigh);
	const sleep = compact({
		match_overlap: num(f.matchOverlap),
		min_episode_coverage: num(f.minEpisodeCoverage),
		include_naps: f.includeNaps === '' ? undefined : f.includeNaps === 'true',
		night_anchor: f.nightAnchor.trim()
	});
	const r: Rule = {
		schema: 'vitamux.rule/1',
		metric: f.metric,
		window: f.windowKind === 'bucket' ? { kind: 'bucket', size: f.bucketSize } : { kind: f.windowKind },
		groups: f.groups.map((g) => ({ id: g.id.trim(), match: g.match.map(cleanSelector) })),
		exclude: f.exclude.length ? f.exclude.map(cleanSelector) : undefined,
		within_source: compact({
			intra_group: f.intraGroup,
			daily_value_policy: f.dailyValuePolicy,
			statistic: f.statistic,
			span: f.statistic === 'min_rolling_mean' ? f.span.trim() : undefined
		}),
		strategy: compact({
			op: f.op,
			min_sources: pooling ? num(f.minSources) : undefined,
			on_insufficient: pooling ? f.onInsufficient : undefined
		}) as Rule['strategy'],
		quality: compact({
			min_coverage: num(f.minCoverage),
			plausible_range: low !== undefined || high !== undefined ? [low, high] : undefined,
			exclude_flags: f.excludeFlags.length ? [...f.excludeFlags] : undefined,
			max_staleness: f.maxStaleness.trim(),
			require_wear: f.requireWear.trim(),
			sleep
		}) as Rule['quality'],
		contexts: f.contexts,
		follow: f.follow.trim() || undefined,
		compose: f.compose ? { from: 'hour', op: f.compose } : undefined,
		acknowledged_warnings: f.acknowledged.length ? [...f.acknowledged] : undefined
	};
	return JSON.parse(JSON.stringify(r)); // drops undefined keys
}

/** "a › b › c": the group ids in ladder order. */
export const ladder = (r: Rule) => r.groups.map((g) => g.id).join(' › ');

/** One readable line per selector: "provider apple_health, device type watch, not relayed". */
export function selectorText(s: Selector): string {
	return Object.entries(s)
		.map(([k, v]) => {
			if (k === 'relayed') return v ? 'relayed' : 'not relayed';
			return `${selectorLabels[k as SelectorField]?.toLowerCase() ?? k} ${v}`;
		})
		.join(', ');
}

export interface Change {
	path: string;
	before?: unknown;
	after?: unknown;
}

function flatten(v: unknown, path: string, out: Map<string, unknown>) {
	if (v !== null && typeof v === 'object') {
		const entries = Array.isArray(v) ? v.map((x, i) => [`[${i}]`, x] as const) : Object.entries(v).map(([k, x]) => [`.${k}`, x] as const);
		if (!entries.length) out.set(path, v);
		for (const [k, x] of entries) flatten(x, path + k, out);
		return;
	}
	out.set(path, v);
}

/** Field-level differences between two rule specs, in the first spec's field order. */
export function diffSpecs(a: unknown, b: unknown): Change[] {
	const fa = new Map<string, unknown>();
	const fb = new Map<string, unknown>();
	flatten(a, '', fa);
	flatten(b, '', fb);
	const paths = [...fa.keys(), ...[...fb.keys()].filter((k) => !fa.has(k))];
	const out: Change[] = [];
	for (const p of paths) {
		const before = fa.get(p);
		const after = fb.get(p);
		if (JSON.stringify(before) !== JSON.stringify(after)) out.push({ path: p.replace(/^\./, ''), before, after });
	}
	return out;
}

/** Local dates (YYYY-MM-DD) `days` long, ending today in the browser's timezone. */
export function lastDays(days: number): { start: string; end: string } {
	const iso = (d: Date) =>
		`${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
	const end = new Date();
	const start = new Date(end.getFullYear(), end.getMonth(), end.getDate() - (days - 1));
	return { start: iso(start), end: iso(end) };
}
