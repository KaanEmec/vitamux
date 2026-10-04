<!--
	Metric detail: the view comes from the catalogue (chartFor), so no metric has its own code.
	A stats header (latest, period mean, range, coverage; GET /resolved/summary with compare=true for
	the neutral deltas), then the chart: up to a year one resolved value per day (GET /resolved/daily)
	with status markers, a 7-day range band, the period mean, each source's own values as toggles
	(GET /sources/series), the previous period as a dashed overlay, the source behind each day and
	a brush navigator; "All" plots weekly or monthly rollups (GET /resolved/trend). Below: the
	distribution and the values table. Every day opens its explanation, inputs, provenance and
	overrides (PointPanel) and the all-sources day view, also from the chart's pinned tooltip.
	The rule lens (lib/rules/RuleLens.svelte) is the side panel; it overlays a draft rule as a ghost.
	Query: ?range=1W|1M|3M|1Y|All&end=YYYY-MM-DD (shareable).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { providerLabel } from '#lib/connections/connections.ts';
	import { chartFor } from '#lib/charts/grammar.ts';
	import RangePicker, { rangeStart, type RangeKey } from '#lib/charts/RangePicker.svelte';
	import type { Series } from '#lib/charts/types.ts';
	import { addDays, datesDescending, isDate, metricLabel, today } from '#lib/data/format.ts';
	import MetricStats from '#lib/explore/MetricStats.svelte';
	import PointPanel from '#lib/explore/PointPanel.svelte';
	import { Pins } from '#lib/explore/pins.svelte.ts';
	import { dayGroup, dayProviders, num, sourceSeries } from '#lib/explore/series.ts';
	import ValuesTable from '#lib/explore/ValuesTable.svelte';
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import { metricLook } from '#lib/ui/metric.ts';
	import MetricTile from '#lib/ui/MetricTile.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { sourceClass } from '#lib/ui/source.ts';
	import { displayStatus } from '#lib/ui/status.ts';
	import { dayLabel, dayMs, mean as meanOf } from '#lib/views/format.ts';

	type Resolved = Schemas['ResolvedValue'];
	type Days = Record<string, Resolved | undefined>;
	type Draft = { local_date: string; value: number | null; changed: boolean }[];

	const ranges: RangeKey[] = ['1W', '1M', '3M', '1Y', 'All'];
	const rangeDays: Partial<Record<RangeKey, 7 | 30 | 90 | 365>> = { '1W': 7, '1M': 30, '3M': 90, '1Y': 365 };

	const metric = $derived(page.params.metric ?? '');
	const range = $derived.by((): RangeKey => {
		const r = page.url.searchParams.get('range') as RangeKey;
		return ranges.includes(r) ? r : '3M';
	});
	const end = $derived.by(() => {
		const e = page.url.searchParams.get('end');
		return isDate(e) ? e : today();
	});
	/** Days in the range; null for All. */
	const span = $derived(rangeDays[range] ?? null);

	let meta = $state<Schemas['Metric'] | null>(null);
	let metaProblem = $state<Problem | null>(null);
	let summary = $state<Schemas['MetricSummary'] | null>(null);
	let daily = $state<Days | null>(null);
	let trend = $state<Schemas['ResolvedTrend'] | null>(null);
	let seriesProblem = $state<Problem | null>(null);
	let loading = $state(true);
	let sources = $state<Schemas['SourceSeries'] | null>(null);
	let sourcesProblem = $state<Problem | null>(null);
	let previous = $state<Days | null>(null);
	/** Bumped after an override or a rule change, to load the values again. */
	let version = $state(0);

	let showBaseline = $state(true);
	let shownSources = $state<string[]>([]);
	let compare = $state(false);
	let lensOpen = $state(false);
	let draft = $state<Draft | null>(null);
	let selected = $state<string | null>(null);
	/** The chart's zoom, shared with the navigator under it; reset with the range. */
	let zoomed = $state<[number, number] | null>(null);
	const pins = new Pins();

	onMount(() => void pins.load());

	$effect(() => {
		const m = metric;
		meta = summary = null;
		metaProblem = null;
		selected = null;
		void api.GET('/api/v1/metrics/{code}', { params: { path: { code: m } } }).then((res) => {
			if (m !== metric) return;
			meta = res.data ?? null;
			metaProblem = res.error ?? null;
		});
	});

	$effect(() => {
		const [m, e] = [metric, end];
		void version;
		void api.GET('/api/v1/resolved/summary', { params: { query: { metrics: [m], date: e, compare: true } } }).then((res) => {
			if (m === metric && e === end) summary = res.data?.metrics[m] ?? null;
		});
	});

	let seriesGen = 0;
	$effect(() => {
		const [m, r, e] = [metric, range, end];
		void version;
		const mine = ++seriesGen;
		loading = true;
		if (r === 'All') daily = null;
		else trend = null;
		zoomed = null;
		void loadSeries(m, r, e).then((out) => {
			if (mine !== seriesGen) return;
			({ daily, trend } = out);
			seriesProblem = out.problem;
			loading = false;
		});
	});

	const getDays = async (m: string, start: string, e: string) => {
		const res = await api.GET('/api/v1/resolved/daily', { params: { query: { start_date: start, end_date: e, metrics: [m] } } });
		return { days: res.data ? (Object.fromEntries(res.data.days.map((d) => [d.local_date, d.metrics[m]])) as Days) : null, problem: res.error ?? null };
	};

	async function loadSeries(m: string, r: RangeKey, e: string) {
		const start = rangeStart(r, e);
		if (start) {
			const { days, problem } = await getDays(m, start, e);
			return { daily: days, trend: null, problem };
		}
		// All: monthly rollups over ten years; up to two years of data read better by week.
		const get = (from: string, grain: 'week' | 'month') =>
			api.GET('/api/v1/resolved/trend', { params: { query: { metric: m, start_date: from, end_date: e, grain } } });
		let res = await get(addDays(e, -3659), 'month');
		const first = res.data?.buckets.find((b) => b.n > 0);
		if (first && first.start_date > addDays(e, -730)) res = await get(first.start_date, 'week');
		const data = res.data && { ...res.data, buckets: res.data.buckets.slice(res.data.buckets.findIndex((b) => b.n > 0)) };
		return { daily: null, trend: data && data.buckets.length && data.buckets[0].n > 0 ? data : null, problem: res.error ?? null };
	}

	/** First local date on the chart: the range's, or the first rollup with data. */
	const from = $derived(rangeStart(range, end) ?? trend?.buckets[0]?.start_date ?? null);
	const dates = $derived(range === 'All' || !from ? [] : datesDescending(from, end).reverse());

	$effect(() => {
		const [m, s, e] = [metric, from, end];
		sources = null;
		sourcesProblem = null;
		if (!s) return;
		// Whole local days with a day of margin either side, within the endpoint's ten years.
		const first = s < addDays(e, -3655) ? addDays(e, -3655) : s;
		void api
			.GET('/api/v1/sources/series', {
				params: { query: { metric: m, start: `${addDays(first, -1)}T00:00:00Z`, end: `${addDays(e, 2)}T00:00:00Z`, grain: 'day' } }
			})
			.then((res) => {
				if (m !== metric || s !== from || e !== end) return;
				sources = res.data ?? null;
				sourcesProblem = res.error ?? null;
			});
	});

	// The period before the range, for the "Compare previous" overlay.
	$effect(() => {
		const [m, s, n, on] = [metric, from, span, compare];
		void version;
		previous = null;
		if (!on || !n || !s) return;
		void getDays(m, addDays(s, -n), addDays(s, -1)).then(({ days }) => {
			if (m === metric && s === from && compare) previous = days;
		});
	});

	const label = $derived(metricLabel(metric));
	const look = $derived(metricLook(metric, meta?.section));
	const view = $derived(meta ? chartFor(meta) : 'line-baseline');
	const unit = $derived(meta?.unit ?? summary?.unit ?? trend?.unit ?? '');

	const resolved = $derived.by((): Series | null => {
		if (trend) {
			return {
				label: trend.grain === 'week' ? 'Weekly mean' : 'Monthly mean',
				xs: trend.buckets.map((b) => dayMs(b.start_date)),
				ys: trend.buckets.map((b) => b.mean ?? null)
			};
		}
		if (!daily) return null;
		const values = dates.map((d) => daily?.[d]);
		return {
			label: 'Resolved',
			xs: dates.map(dayMs),
			ys: values.map((v) => (v ? num(v.value, metric) : null)),
			status: values.map((v) => displayStatus(v?.status ?? 'no_data', v?.partial)),
			providers: values.map((v) => dayProviders(v))
		};
	});

	const values = $derived((resolved?.ys ?? []).filter((v) => v != null));
	const cmp = $derived(summary?.comparisons?.find((c) => c.days === span));
	const mean = $derived.by(() => {
		if (trend) {
			const n = trend.buckets.reduce((a, b) => a + (b.mean == null ? 0 : b.n), 0);
			return n ? trend.buckets.reduce((a, b) => a + (b.mean ?? 0) * b.n, 0) / n : null;
		}
		return cmp?.current.mean ?? meanOf(values);
	});
	const meanLabel = $derived(span ? `${span}-day mean` : 'Mean');

	/** A 7-day range around the line (rollups: each one's min–max); gaps stay gaps. */
	const band = $derived.by(() => {
		if (!showBaseline || !resolved?.xs.length) return undefined;
		if (trend) {
			return { xs: resolved.xs, lo: trend.buckets.map((b) => b.min ?? null), hi: trend.buckets.map((b) => b.max ?? null), label: `${trend.grain === 'week' ? 'Weekly' : 'Monthly'} min–max` };
		}
		const ys = resolved.ys;
		const roll = (i: number, f: (...v: number[]) => number) => {
			const w = ys.slice(Math.max(0, i - 6), i + 1).filter((v): v is number => v != null);
			return ys[i] == null || !w.length ? null : f(...w);
		};
		return { xs: resolved.xs, lo: ys.map((_, i) => roll(i, Math.min)), hi: ys.map((_, i) => roll(i, Math.max)), label: '7-day range' };
	});
	const baseline = $derived(showBaseline && mean != null ? { value: mean, label: meanLabel } : undefined);

	const providers = $derived([...new Set(sources?.sources.map((s) => s.provider) ?? [])]);
	const overlay = $derived(sources && from ? sourceSeries(sources, from, end).filter((s) => s.source && shownSources.includes(s.source)) : []);
	const before = $derived.by((): Series[] => {
		if (!previous || !span || trend) return [];
		const p = previous;
		return [{ label: `Previous ${span} days`, xs: dates.map(dayMs), ys: dates.map((d) => num(p[addDays(d, -span)]?.value, metric)) }];
	});
	const ghost = $derived.by((): Series[] => {
		if (!draft) return [];
		const changed = draft.filter((d) => d.changed);
		return [
			{ label: 'Draft rule', style: 'ghost', xs: draft.map((d) => dayMs(d.local_date)), ys: draft.map((d) => d.value) },
			{ label: 'Changed by the draft', style: 'dots', xs: changed.map((d) => dayMs(d.local_date)), ys: changed.map((d) => d.value) }
		];
	});
	const series = $derived(resolved ? [resolved, ...before, ...overlay, ...ghost] : []);
	// Additive metrics are bars per day unless lines are overlaid on them.
	const bars = $derived((view === 'bars' || view === 'sleep') && !trend && series.length === 1);

	/** The source behind each day's value, as one strip. */
	const strip = $derived.by(() => {
		if (!daily || !dates.length) return [];
		const by = dates.map((d) => dayProviders(daily?.[d])[0] ?? null);
		return [{ label: 'Source per day', days: by.map((s) => (s ? 1 : 0)), sources: by }];
	});
	/** Days each rule group supplied, for the lens. */
	const counts = $derived.by(() => {
		const n: Record<string, number> = {};
		for (const d of dates) {
			const v = daily?.[d];
			const g = v?.status === 'no_data' ? '' : dayGroup(v);
			if (g) n[g] = (n[g] ?? 0) + 1;
		}
		return n;
	});

	const rule = $derived(summary?.rule ?? Object.values(daily ?? {}).find((v) => v?.rule)?.rule);
	const spanText = $derived(from ? `${dayLabel(from)} – ${dayLabel(end)}` : '');

	function setRange(key: RangeKey) {
		const q = new URLSearchParams({ ...Object.fromEntries(page.url.searchParams), range: key });
		void goto(`?${q}`, { replace: true, reset: false });
	}

	function toggleSource(p: string) {
		shownSources = shownSources.includes(p) ? shownSources.filter((x) => x !== p) : [...shownSources, p];
	}

	function toggleLens() {
		lensOpen = !lensOpen;
		if (!lensOpen) draft = null;
	}

	/** Below the side-by-side layout the lens stacks under the chart: bring it into view. */
	const reveal = (node: HTMLElement) => {
		if (matchMedia('(max-width: 63.99rem)').matches) node.scrollIntoView({ block: 'start' });
	};

	/** A plotted day opens its panel; a rollup opens its week or month. */
	function select(i: number) {
		if (!trend) {
			selected = dates[i] ?? null;
			return;
		}
		const b = trend.buckets[i];
		const q = new URLSearchParams({ range: trend.grain === 'week' ? '1W' : '1M', end: b.end_date });
		void goto(`?${q}`);
	}

	/** The tooltip's actions on a plotted day: its explanation and overrides (PointPanel), or its records. */
	const actions = $derived(
		trend
			? undefined
			: [
					{ label: 'Explain', run: select },
					{ label: 'Override', run: select },
					{ label: 'Raw records', run: (i: number) => void goto(`/explore/${encodeURIComponent(metric)}/day/${dates[i]}`) }
				]
	);
</script>

<svelte:head><title>{label} · Vitamux</title></svelte:head>

<nav class="crumbs" aria-label="Breadcrumb">
	<a href="/explore">Explore</a>
	{#if meta}<span aria-hidden="true">/</span><span>{meta.section}</span>{/if}
	<span aria-hidden="true">/</span><span aria-current="page">{label}</span>
</nav>

{#if metaProblem?.status === 404}
	<h1>{label}</h1>
	<EmptyState icon={icons.explore} title="No metric with the code {metric}" text="It is not in the catalogue. Explore lists everything Vitamux has stored.">
		<Button href="/explore">Explore</Button>
	</EmptyState>
{:else}
	<header class="head">
		<div class="title">
			<MetricTile code={metric} section={meta?.section} size="lg" />
			<div>
				<h1>{label}</h1>
				<p class="sub">
					<span class="code">{metric}</span>{meta ? ` · ${meta.aggregation.replaceAll('_', ' ')}` : ''}{unit ? ` · ${unit}` : ''}
					{#if rule}
						· <a href="/rules/{encodeURIComponent(metric)}">{rule.ref.startsWith('builtin:') ? 'Built-in rule' : `Rule v${rule.version}`}{rule.strategy ? `, ${rule.strategy.replaceAll('_', ' ')}` : ''}</a>
					{/if}
				</p>
			</div>
		</div>
		<div class="actions">
			<RangePicker value={range} options={ranges} onchange={setRange} />
			<button class="btn" type="button" aria-pressed={compare} disabled={!span} onclick={() => (compare = !compare)}>
				<Icon d={icons.metric} size={16} />Compare previous
			</button>
			{#if pins.layout}
				<button class="btn ghost" type="button" aria-pressed={pins.has(metric)} onclick={() => pins.toggle(metric)}>
					<span class={['star', pins.has(metric) && 'pinned']}><Icon d={icons.star} size={16} /></span>
					{pins.has(metric) ? 'Pinned' : 'Pin to dashboard'}
				</button>
			{/if}
			<button class="btn lens-btn" type="button" aria-expanded={lensOpen} onclick={toggleLens}>
				<Icon d={icons.rules} size={16} />How it’s calculated
			</button>
		</div>
	</header>

	<ProblemAlert problem={metaProblem} />
	<ProblemAlert problem={pins.problem} />

	{#if view === 'dumbbell'}
		<p class="muted">Readings are paired with their other values in the <a href="/explore/blood-pressure">blood pressure view</a>.</p>
	{:else if view === 'sleep'}
		<p class="muted">Stages and nights side by side are in the <a href="/explore/sleep">sleep view</a>.</p>
	{/if}

	<div class={['layout', lensOpen && 'with-lens']} style:--metric={look.color}>
		<div class="main">
			<section class="card chart" aria-label="{label} chart">
				<MetricStats {metric} {summary} {trend} {values} days={dates.length} {span} {unit} {mean} previousMean={cmp?.previous.mean} />

				<div class="toggles" role="group" aria-label="Series">
					<span class="muted small">Show</span>
					<span class="chip resolved"><span class="dot" aria-hidden="true"></span>Resolved</span>
					{#each providers as p (p)}
						{@const shown = shownSources.includes(p)}
						<button type="button" class={['chip', sourceClass(p), !shown && 'dashed']} aria-pressed={shown} onclick={() => toggleSource(p)}>
							<span class="dot" aria-hidden="true"></span>{providerLabel(p)}
						</button>
					{/each}
					<button type="button" class={['chip', !showBaseline && 'dashed']} aria-pressed={showBaseline} onclick={() => (showBaseline = !showBaseline)}>Range and mean</button>
					<span class="muted small span">{spanText}{resolved && !bars ? ' · drag on the chart to zoom' : ''}</span>
				</div>

				<ProblemAlert problem={seriesProblem} />
				<ProblemAlert problem={sourcesProblem} />
				{#if loading}
					<Skeleton variant="chart" label="Loading values" />
				{:else if resolved && values.length}
					{#if bars}
						{#await import('#lib/charts/Bars.svelte')}
							<Skeleton variant="chart" label="Loading chart" />
						{:then { default: Bars }}
							<Bars
								xs={resolved.xs}
								stacks={[{ label: 'Resolved', ys: resolved.ys }]}
								status={resolved.status}
								label="{label}, resolved per day"
								{unit}
								timezone="UTC"
								{baseline}
								{actions}
								onselect={select}
							/>
						{/await}
					{:else}
						{#await import('#lib/charts/TimeSeries.svelte')}
							<Skeleton variant="chart" label="Loading chart" />
						{:then { default: TimeSeries }}
							<TimeSeries
								{series}
								label="{label}, {trend ? resolved.label.toLowerCase() : 'resolved per day'}"
								{unit}
								timezone="UTC"
								withTime={false}
								height={320}
								step={view === 'step' || view === 'dumbbell'}
								{band}
								{baseline}
								area={view !== 'step' && view !== 'dumbbell'}
								bind:view={zoomed}
								{actions}
								onselect={select}
							/>
						{/await}
					{/if}
					{#if strip.length}
						{#await import('#lib/charts/CoverageStrip.svelte') then { default: CoverageStrip }}
							<CoverageStrip rows={strip} start={dates[0]} caption="{label}: the source behind each day's value" />
						{/await}
					{/if}
					{#if !bars && resolved.xs.length > 14}
						{#await import('#lib/charts/BrushNavigator.svelte') then { default: BrushNavigator }}
							<BrushNavigator xs={resolved.xs} ys={resolved.ys} bind:view={zoomed} label="{label} range" timezone="UTC" />
						{/await}
					{/if}
				{:else if !seriesProblem}
					<EmptyState title="No values in this range" text="Choose a longer range, or check the sources on the Connections page." />
				{/if}
			</section>

			{#if selected}
				<PointPanel {metric} date={selected} value={daily?.[selected]} onchanged={() => version++} onclose={() => (selected = null)} />
			{/if}

			<div class="below">
				{#if values.length > 2}
					<section class="card dist" aria-labelledby="dist-h">
						<div class="card-head">
							<h2 id="dist-h">Distribution</h2>
							<span class="muted small">{values.length} {trend ? (trend.grain === 'week' ? 'weeks' : 'months') : 'days'} · {range}</span>
						</div>
						{#await import('#lib/charts/Histogram.svelte') then { default: Histogram }}
							<Histogram values={resolved?.ys ?? []} label="{label}, distribution in range" {unit} height={180} noun={trend ? (trend.grain === 'week' ? 'weeks' : 'months') : 'days'} />
						{/await}
					</section>
				{/if}

				<ValuesTable {metric} {unit} {trend} {dates} {daily} {selected} onselect={(d) => (selected = d)} />
			</div>
		</div>

		{#if lensOpen && from}
			<div class="lens" {@attach reveal}>
				{#await import('#lib/rules/RuleLens.svelte') then { default: RuleLens }}
					<RuleLens {metric} start={from} {end} inline {counts} ondraft={(d) => (draft = d)} onsaved={() => version++} />
				{/await}
			</div>
		{/if}
	</div>
{/if}

<style>
	.crumbs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-bottom: var(--space-4);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.crumbs a {
		color: var(--color-text-muted);
		text-decoration: none;
	}
	.crumbs a:hover {
		color: var(--color-text);
	}
	[aria-current='page'] {
		color: var(--color-text);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}
	.title {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-width: 0;
	}
	.title h1 {
		margin: 0;
	}
	.sub {
		margin: var(--space-1) 0 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.code {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.btn[aria-pressed='true'] {
		background: var(--color-selected);
	}
	.lens-btn {
		color: var(--color-link);
		background: var(--color-accent-soft);
		border-color: color-mix(in srgb, var(--color-accent) 35%, transparent);
	}
	.lens-btn[aria-expanded='true'] {
		border-color: var(--color-accent);
	}
	.star {
		display: inline-flex;
	}
	.star.pinned {
		color: var(--color-accent);
	}
	.star.pinned :global(path) {
		fill: currentColor;
	}
	.layout {
		display: grid;
		gap: var(--space-5);
	}
	@media (min-width: 64rem) {
		.layout.with-lens {
			grid-template-columns: minmax(0, 1fr) minmax(20rem, 23rem);
			align-items: start;
		}
	}
	.main {
		display: grid;
		gap: var(--space-5);
		min-width: 0;
	}
	.lens {
		min-width: 0;
		scroll-margin-top: var(--space-4);
	}
	.chart {
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	.small {
		font-size: var(--text-xs);
	}
	.toggles {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		padding-top: var(--space-4);
		border-top: 1px solid var(--color-border);
	}
	.resolved {
		--tone: var(--metric, var(--color-accent));
	}
	.span {
		flex: 1 1 12rem;
		text-align: right;
	}
	.below {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-5);
		align-items: flex-start;
	}
	.dist {
		display: grid;
		flex: 1 1 18rem;
		gap: var(--space-3);
		min-width: 0;
	}
	.card-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
	}
	.card-head h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	@media (max-width: 40rem) {
		.span {
			text-align: left;
		}
	}
</style>
