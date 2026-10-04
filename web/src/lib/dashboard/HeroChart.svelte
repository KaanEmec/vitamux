<!--
	The hero chart: the selected stat tile's metric over 7, 30 or 90 days (GET /resolved/series, one
	resolved value per local day with its status and providers) or a year of weekly means
	(GET /resolved/trend). The grammar picks the view: bars for additive metrics, a line with a
	7-day range for intensive ones, a step line with readings for latest ones. A mean line, the
	period's range and a neutral delta against the period before it (GET /resolved/summary
	comparisons), the sources and a link to Explore. Gaps stay gaps.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import { chartFor } from '../charts/grammar.ts';
	import { addDays, datesDescending } from '../data/format.ts';
	import { dayMs, num } from '../explore/series.ts';
	import { metricHref } from '../nav.ts';
	import Chip from '../ui/Chip.svelte';
	import { metricLook } from '../ui/metric.ts';
	import Segmented from '../ui/Segmented.svelte';
	import Skeleton from '../ui/Skeleton.svelte';
	import { displayStatus, type DataStatus } from '../ui/status.ts';
	import { providerName } from '../views/format.ts';
	import { cardLabel, type Catalogue } from './layout.ts';
	import { cardView, lead, periods, periodView, periodWord, show, unitText, type Period, type Summary } from './summary.ts';

	let {
		code,
		meta,
		summary,
		day,
		period = $bindable('30D')
	}: {
		code: string;
		/** The catalogue entry (absent for a rule family). */
		meta?: Catalogue;
		summary?: Summary;
		/** The last local date shown. */
		day: string;
		period?: Period;
	} = $props();

	interface Data {
		/** The metric these values are of: a period switch keeps the chart (and animates it), a metric switch does not. */
		code: string;
		xs: number[];
		ys: (number | null)[];
		lo: (number | null)[];
		hi: (number | null)[];
		status?: (DataStatus | null)[];
		providers?: (string[] | null)[];
		dates?: string[];
		weekly: boolean;
	}

	const charts = { bars: import('../charts/Bars.svelte'), line: import('../charts/TimeSeries.svelte') };
	let loaded = $state<Data | null>(null);
	let problem = $state<Problem | null>(null);
	let width = $state(1024);

	const view = $derived(code === 'sleep' ? 'sleep' : meta ? chartFor(meta) : 'line-baseline');
	const bars = $derived(view === 'bars' || view === 'sleep');
	const unit = $derived(code === 'sleep' ? 's' : (meta?.unit ?? summary?.unit));
	const label = $derived(cardLabel(code));
	const stats = $derived(summary ? periodView(code, summary, period) : null);
	const value = (v: unknown) => num(v, lead[code] ?? code);

	let gen = 0;
	$effect(() => {
		const [c, p, d, m] = [code, period, day, meta];
		const mine = ++gen;
		problem = null;
		void load(c, p, d, m).then((r) => {
			if (mine !== gen) return;
			loaded = r.data;
			problem = r.problem;
		});
	});

	async function load(c: string, p: Period, d: string, m?: Catalogue): Promise<{ data: Data | null; problem: Problem | null }> {
		const rolled = (r?: Schemas['Rollup']) => (r && c in lead ? r.components?.[lead[c]] : r);
		if (p === '1Y') {
			const res = await api.GET('/api/v1/resolved/trend', { params: { query: { metric: c, start_date: addDays(d, -363), end_date: d, grain: 'week' } } });
			if (!res.data) return { data: null, problem: res.error };
			const b = res.data.buckets.map(rolled);
			return {
				data: {
					code: c,
					xs: res.data.buckets.map((x) => dayMs(x.start_date)),
					ys: b.map((x) => x?.mean ?? null),
					lo: b.map((x) => x?.min ?? null),
					hi: b.map((x) => x?.max ?? null),
					weekly: true
				},
				problem: null
			};
		}
		const first = addDays(d, 1 - periods[p]);
		const window = c === 'sleep' || (m && !m.windows.includes('local_day') && m.windows.includes('local_night')) ? 'local_night' : m ? 'local_day' : undefined;
		const res = await api.GET('/api/v1/resolved/series', {
			params: { query: { metric: c, start: `${addDays(first, -1)}T00:00:00Z`, end: `${addDays(d, 2)}T00:00:00Z`, window, limit: 400 } }
		});
		if (!res.data) return { data: null, problem: res.error };
		const byDate = new Map(res.data.points.flatMap((pt) => (pt.local_date ? [[pt.local_date, pt] as const] : [])));
		const dates = datesDescending(first, d).reverse();
		const points = dates.map((x) => byDate.get(x));
		const ys = points.map((pt) => (pt ? value(pt.value) : null));
		// The 7-day range: min and max of the values in each trailing week.
		const week = (i: number) => ys.slice(Math.max(0, i - 6), i + 1).filter((y): y is number => y != null);
		return {
			data: {
				code: c,
				xs: dates.map(dayMs),
				ys,
				lo: ys.map((_, i) => (week(i).length ? Math.min(...week(i)) : null)),
				hi: ys.map((_, i) => (week(i).length ? Math.max(...week(i)) : null)),
				status: points.map((pt) => (pt ? displayStatus(pt.status, pt.partial) : null)),
				providers: points.map((pt) => pt?.providers ?? null),
				dates,
				weekly: false
			},
			problem: null
		};
	}

	const data = $derived(loaded?.code === code ? loaded : null);
	const mean = $derived.by(() => {
		if (stats?.mean != null) return stats.mean;
		const v = data?.ys.filter((y): y is number => y != null) ?? [];
		return v.length ? v.reduce((a, b) => a + b, 0) / v.length : undefined;
	});
	const baseline = $derived(mean == null ? undefined : { value: mean, label: `${periodWord(period)} mean` });
	const hasValues = $derived(!!data?.ys.some((y) => y != null));
	const sources = $derived.by(() => {
		const seen = [...new Set(data?.providers?.flatMap((p) => p ?? []) ?? [])];
		if (seen.length) return seen.map((p) => ({ provider: p, label: providerName(p) }));
		return summary ? cardView(code, summary, bars).chips : [];
	});
	const fmt = (v: number) => `${show(v, unit)}${unitText(unit) ? ` ${unitText(unit)}` : ''}`;
	const actions = $derived(
		data?.dates ? [{ label: 'All sources of this day', run: (i: number) => void goto(`/explore/${code}/day/${data?.dates?.[i]}`) }] : []
	);
	const height = $derived(width < 640 ? 200 : 280);
	const chartLabel = $derived(`${label}, ${data?.weekly ? 'weekly means of the last year' : `daily values of the last ${periods[period]} days`}`);
</script>

<svelte:window bind:innerWidth={width} />

<section class="card hero" aria-labelledby="hero-title" style:--metric={metricLook(code, meta?.section).color}>
	<div class="head">
		<div class="title">
			<h2 id="hero-title"><span class="dot" aria-hidden="true"></span>{label}</h2>
			<p class="muted">
				{data?.weekly ? 'Weekly means of daily values' : 'Daily values'}{summary?.rule?.version ? ` · rule v${summary.rule.version}` : ''}
			</p>
		</div>
		<Segmented label="Period" options={Object.keys(periods).map((p) => ({ value: p as Period, label: p }))} bind:value={period} />
	</div>

	<dl class="stats">
		<div>
			<dt>{periodWord(period)} mean</dt>
			<dd>{mean == null ? '–' : show(mean, unit)}{#if mean != null && unitText(unit)}<span class="unit">{unitText(unit)}</span>{/if}</dd>
			{#if stats?.delta}<dd class="delta">{stats.delta}</dd>{/if}
		</div>
		<div>
			<dt>Range</dt>
			<dd>{stats?.min != null && stats.max != null ? `${show(stats.min, unit)}–${show(stats.max, unit)}` : '–'}</dd>
			<dd class="delta">min – max</dd>
		</div>
	</dl>

	{#if problem}
		{#if problem.status === 404 || problem.status === 503}<p class="muted">The chart is not available yet.</p>{:else}<ProblemAlert {problem} />{/if}
	{:else if !data}
		<Skeleton variant="chart" />
	{:else if !hasValues}
		<p class="empty muted">No values in the last {period === '1Y' ? 'year' : `${periods[period]} days`}.</p>
	{:else if bars}
		{#await charts.bars then { default: Bars }}
			<Bars
				xs={data.xs}
				stacks={[{ label, ys: data.ys }]}
				label={chartLabel}
				timezone="UTC"
				{height}
				{baseline}
				status={data.status}
				providers={data.providers}
				format={fmt}
				{actions}
			/>
		{/await}
	{:else}
		{#await charts.line then { default: TimeSeries }}
			<TimeSeries
				series={[{ label: data.weekly ? 'Weekly mean' : label, xs: data.xs, ys: data.ys, status: data.status, providers: data.providers }]}
				label={chartLabel}
				unit={unitText(unit)}
				timezone="UTC"
				{height}
				step={view === 'step' && !data.weekly}
				area={view !== 'step'}
				band={view === 'line-band' || data.weekly ? { xs: data.xs, lo: data.lo, hi: data.hi, label: data.weekly ? 'Weekly min–max' : '7-day range' } : undefined}
				{baseline}
				zoom={false}
				withTime={false}
				{actions}
			/>
		{/await}
	{/if}

	<footer>
		{#if sources.length}
			<span class="sources">Sources {#each sources as s (s.label)}<Chip source={s.provider}>{s.label}</Chip>{/each}</span>
		{/if}
		<a href={metricHref(code)}>Open in Explore<span class="visually-hidden"> {label}</span> →</a>
	</footer>
</section>

<style>
	.hero {
		display: flex;
		flex: 999 1 34rem;
		flex-direction: column;
		gap: var(--space-4);
		min-width: 0;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3);
	}
	h2 {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin: 0 0 var(--space-1);
		font-size: var(--text-lg);
	}
	.title p {
		margin: 0;
		font-size: var(--text-sm);
	}
	.dot {
		width: 0.625rem;
		height: 0.625rem;
		background: var(--metric);
		border-radius: var(--radius-xs);
	}
	.stats {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3) var(--space-6);
		margin: 0;
	}
	.stats div {
		display: grid;
		gap: 2px;
	}
	dt {
		font-size: var(--text-2xs);
		font-weight: 500;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--color-text-muted);
	}
	dd {
		display: flex;
		align-items: baseline;
		gap: var(--space-1);
		margin: 0;
		font-size: var(--text-xl);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.unit,
	.delta {
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--color-text-muted);
	}
	.empty {
		display: grid;
		place-items: center;
		min-height: 12rem;
		margin: 0;
	}
	footer {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		padding-top: var(--space-3);
		font-size: var(--text-sm);
		border-top: 1px solid var(--color-border);
	}
	.sources {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	footer a {
		margin-left: auto;
		font-weight: 500;
		text-decoration: none;
	}
</style>
