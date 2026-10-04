<!--
	Metric detail: the view comes from the catalogue (chartFor), so no metric has its own code.
	Up to a year it plots one resolved value per day (GET /resolved/daily) with status markers;
	"All" plots weekly or monthly rollups (GET /resolved/trend). Overlays: the 30-day baseline
	band (GET /resolved/summary, or each rollup's min–max), each source's own values
	(GET /sources/series) and per-source coverage (GET /coverage), with a brush navigator and the
	distribution of the values. Every day opens its explanation, inputs, provenance and overrides
	(PointPanel) and the all-sources day view, also from the chart's pinned tooltip.
	The rule lens (lib/rules/RuleLens.svelte) overlays a draft rule as a ghost series.
	Query: ?range=1W|1M|3M|1Y|All&end=YYYY-MM-DD (shareable).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ExplainPopover from '#lib/components/ExplainPopover.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ResultStatus from '#lib/components/ResultStatus.svelte';
	import { chartFor } from '#lib/charts/grammar.ts';
	import RangePicker, { rangeStart, type RangeKey } from '#lib/charts/RangePicker.svelte';
	import { formatInstant, formatNumber } from '#lib/charts/scale.ts';
	import type { Series } from '#lib/charts/types.ts';
	import { addDays, datesDescending, formatValue, isDate, metricLabel, today } from '#lib/data/format.ts';
	import PointPanel from '#lib/explore/PointPanel.svelte';
	import { Pins } from '#lib/explore/pins.svelte.ts';
	import { dayMs, num, sourceSeries } from '#lib/explore/series.ts';
	import Badge from '#lib/ui/Badge.svelte';
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { displayStatus } from '#lib/ui/status.ts';

	type Resolved = Schemas['ResolvedValue'];
	type Draft = { local_date: string; value: number | null; changed: boolean }[];

	const ranges: RangeKey[] = ['1W', '1M', '3M', '1Y', 'All'];
	const pageRows = 31;

	const metric = $derived(page.params.metric ?? '');
	const range = $derived.by((): RangeKey => {
		const r = page.url.searchParams.get('range') as RangeKey;
		return ranges.includes(r) ? r : '3M';
	});
	const end = $derived.by(() => {
		const e = page.url.searchParams.get('end');
		return isDate(e) ? e : today();
	});

	let meta = $state<Schemas['Metric'] | null>(null);
	let metaProblem = $state<Problem | null>(null);
	let summary = $state<Schemas['MetricSummary'] | null>(null);
	let daily = $state<Record<string, Resolved | undefined> | null>(null);
	let trend = $state<Schemas['ResolvedTrend'] | null>(null);
	let seriesProblem = $state<Problem | null>(null);
	let loading = $state(true);
	let sources = $state<Schemas['SourceSeries'] | null>(null);
	let sourcesProblem = $state<Problem | null>(null);
	let coverage = $state<Schemas['Coverage'] | null>(null);
	let coverageProblem = $state<Problem | null>(null);
	/** Bumped after an override, to load the values again. */
	let version = $state(0);

	let showBaseline = $state(true);
	let showSources = $state(false);
	let showCoverage = $state(true);
	let lensOpen = $state(false);
	let draft = $state<Draft | null>(null);
	let selected = $state<string | null>(null);
	let rows = $state(pageRows);
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
		const m = metric;
		void version;
		void api.GET('/api/v1/resolved/summary', { params: { query: { metrics: [m] } } }).then((res) => {
			if (m === metric) summary = res.data?.metrics[m] ?? null;
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
		rows = pageRows;
		zoomed = null;
		void loadSeries(m, r, e).then((out) => {
			if (mine !== seriesGen) return;
			({ daily, trend } = out);
			seriesProblem = out.problem;
			loading = false;
		});
	});

	async function loadSeries(m: string, r: RangeKey, e: string) {
		const start = rangeStart(r, e);
		if (start) {
			const res = await api.GET('/api/v1/resolved/daily', { params: { query: { start_date: start, end_date: e, metrics: [m] } } });
			const byDate = res.data ? Object.fromEntries(res.data.days.map((d) => [d.local_date, d.metrics[m]])) : null;
			return { daily: byDate, trend: null, problem: res.error ?? null };
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
		if (!showSources || !s) return;
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

	$effect(() => {
		const [m, s, e] = [metric, rangeStart(range, end), end];
		coverage = null;
		coverageProblem = null;
		if (!showCoverage || !s) return;
		void api.GET('/api/v1/coverage', { params: { query: { start_date: s, end_date: e, metric: [m] } } }).then((res) => {
			if (m !== metric || e !== end) return;
			coverage = res.data ?? null;
			coverageProblem = res.error ?? null;
		});
	});

	const label = $derived(metricLabel(metric));
	const view = $derived(meta ? chartFor(meta) : 'line-baseline');
	const unit = $derived(meta?.unit ?? summary?.unit ?? trend?.unit ?? '');
	const fmt = (v: number | null | undefined) => (v == null ? '–' : `${formatNumber(v)}${unit ? ` ${unit}` : ''}`);

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
			status: values.map((v) => displayStatus(v?.status ?? 'no_data', v?.partial))
		};
	});

	const thirty = $derived(summary?.stats[1]);
	const baseline = $derived(showBaseline && thirty?.mean != null ? { value: thirty.mean, label: '30-day mean' } : undefined);
	const band = $derived.by(() => {
		if (!showBaseline || !resolved?.xs.length) return undefined;
		if (trend) {
			return { xs: resolved.xs, lo: trend.buckets.map((b) => b.min ?? null), hi: trend.buckets.map((b) => b.max ?? null), label: `${trend.grain === 'week' ? 'Weekly' : 'Monthly'} min–max` };
		}
		if (thirty?.min == null || thirty.max == null) return undefined;
		const xs = [resolved.xs[0], resolved.xs[resolved.xs.length - 1]];
		return { xs, lo: [thirty.min, thirty.min], hi: [thirty.max, thirty.max], label: '30-day min–max' };
	});

	const overlay = $derived(showSources && sources && from ? sourceSeries(sources, from, end) : []);
	const ghost = $derived.by((): Series[] => {
		if (!draft) return [];
		const changed = draft.filter((d) => d.changed);
		return [
			{ label: 'Draft rule', style: 'ghost', xs: draft.map((d) => dayMs(d.local_date)), ys: draft.map((d) => d.value) },
			{ label: 'Changed by the draft', style: 'dots', xs: changed.map((d) => dayMs(d.local_date)), ys: changed.map((d) => d.value) }
		];
	});
	const series = $derived(resolved ? [resolved, ...overlay, ...ghost] : []);
	// Additive metrics are bars per day unless lines are overlaid on them.
	const bars = $derived((view === 'bars' || view === 'sleep') && !trend && !overlay.length && !ghost.length);

	const values = $derived((resolved?.ys ?? []).filter((v): v is number => v != null));
	const lowest = $derived(trend ? Math.min(...trend.buckets.flatMap((b) => (b.min == null ? [] : [b.min]))) : Math.min(...values));
	const highest = $derived(trend ? Math.max(...trend.buckets.flatMap((b) => (b.max == null ? [] : [b.max]))) : Math.max(...values));
	const withData = $derived(
		trend ? [trend.buckets.reduce((n, b) => n + b.n, 0), trend.buckets.reduce((n, b) => n + b.days, 0)] : [values.length, dates.length]
	);
	const stats = $derived([
		...(summary?.stats ?? []).map((s) => ({ label: `${s.days}-day mean`, value: fmt(s.mean) })),
		{ label: 'Lowest in range', value: Number.isFinite(lowest) ? fmt(lowest) : '–' },
		{ label: 'Highest in range', value: Number.isFinite(highest) ? fmt(highest) : '–' },
		{ label: 'Days with data', value: `${withData[0].toLocaleString()} / ${withData[1].toLocaleString()}` }
	]);

	const latest = $derived(summary?.sparkline.findLast((p) => num(p.value, metric) != null));
	const rule = $derived(summary?.rule ?? Object.values(daily ?? {}).find((v) => v?.rule)?.rule);
	const coverageRows = $derived((coverage?.rows ?? []).filter((r) => r.metric === metric).map((r) => ({ label: r.source, days: r.days })));
	const day = (d: string) => formatInstant(dayMs(d), 'UTC', false);
	const span = $derived(from ? `${day(from)} – ${day(end)}` : '');

	function setRange(key: RangeKey) {
		const q = new URLSearchParams({ ...Object.fromEntries(page.url.searchParams), range: key });
		void goto(`?${q}`, { replace: true, reset: false });
	}

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

	const warningCodes = (r: Resolved) => (r.warnings ?? []).map((w) => (w.group ? `${w.code} (${w.group})` : w.code));
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
			<h1>{label}</h1>
			<span class="code">{metric}{unit ? ` · ${unit}` : ''}{meta ? ` · ${meta.aggregation.replaceAll('_', ' ')}` : ''}</span>
			{#if latest}
				<p class="latest">
					<strong>{formatValue(num(latest.value, metric), '')}</strong>
					<span class="muted">{unit} · {day(latest.local_date)}</span>
					<ResultStatus status={latest.status} partial={latest.partial} />
				</p>
			{/if}
		</div>
		<div class="actions">
			{#if pins.layout}
				<Button aria-pressed={pins.has(metric)} onclick={() => pins.toggle(metric)}>
					<span class={['star', pins.has(metric) && 'pinned']}><Icon d={icons.star} size={16} /></span>
					{pins.has(metric) ? 'Pinned' : 'Pin to dashboard'}
				</Button>
			{/if}
			<Button variant="primary" aria-expanded={lensOpen} onclick={() => (lensOpen = !lensOpen)}>
				<Icon d={icons.rules} size={16} />How it’s calculated
			</Button>
		</div>
	</header>

	<ProblemAlert problem={metaProblem} />
	<ProblemAlert problem={pins.problem} />

	{#if rule}
		<p class="rule">
			<Badge>{rule.ref.startsWith('builtin:') ? 'Built-in rule' : `Rule v${rule.version}`}</Badge>
			{#if rule.strategy}<span>{rule.strategy.replaceAll('_', ' ')}</span>{/if}
			<a href="/rules/{encodeURIComponent(metric)}">Version history →</a>
		</p>
	{/if}
	{#if view === 'dumbbell'}
		<p class="muted">Readings are paired with their other values in the <a href="/explore/blood-pressure">blood pressure view</a>.</p>
	{:else if view === 'sleep'}
		<p class="muted">Stages and nights side by side are in the <a href="/explore/sleep">sleep view</a>.</p>
	{/if}

	<div class="controls">
		<RangePicker value={range} options={ranges} onchange={setRange} />
		<span class="muted span">{span}{resolved && !bars ? ' · drag on the chart to zoom' : ''}</span>
		<label><input type="checkbox" bind:checked={showBaseline} />Baseline band</label>
		<label><input type="checkbox" bind:checked={showSources} />Show sources</label>
		<label><input type="checkbox" bind:checked={showCoverage} />Coverage</label>
	</div>

	<div class={['layout', lensOpen && 'with-lens']}>
		<div class="main">
			<section class="card chart" aria-label="{label} chart">
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
								step={view === 'step' || view === 'dumbbell'}
								{band}
								{baseline}
								area={view !== 'step' && view !== 'dumbbell'}
								bind:view={zoomed}
								{actions}
								onselect={select}
							/>
							{#if resolved.xs.length > 14}
								{#await import('#lib/charts/BrushNavigator.svelte') then { default: BrushNavigator }}
									<BrushNavigator xs={resolved.xs} ys={resolved.ys} bind:view={zoomed} label="{label} range" timezone="UTC" />
								{/await}
							{/if}
						{/await}
					{/if}
				{:else if !seriesProblem}
					<EmptyState title="No values in this range" text="Choose a longer range, or check the sources on the Connections page." />
				{/if}
				{#if showCoverage}
					{#if !rangeStart(range, end)}
						<p class="muted small">Coverage is shown for ranges up to a year.</p>
					{:else if coverageRows.length && coverage}
						<h2 class="sub">Coverage per source</h2>
						{#await import('#lib/charts/CoverageStrip.svelte') then { default: CoverageStrip }}
							<CoverageStrip rows={coverageRows} start={coverage.start_date} caption="{label} coverage per source and day" />
						{/await}
					{/if}
					<ProblemAlert problem={coverageProblem} />
				{/if}
			</section>

			{#if values.length > 2}
				<section class="card chart" aria-labelledby="dist-h">
					<h2 id="dist-h" class="sub">Distribution</h2>
					{#await import('#lib/charts/Histogram.svelte') then { default: Histogram }}
						<Histogram values={resolved?.ys ?? []} label="{label}, distribution in range" {unit} noun={trend ? (trend.grain === 'week' ? 'weeks' : 'months') : 'days'} />
					{/await}
				</section>
			{/if}

			{#if selected}
				<PointPanel {metric} date={selected} value={daily?.[selected]} onchanged={() => version++} onclose={() => (selected = null)} />
			{/if}

			<ul class="stats" aria-label="Statistics">
				{#each stats as s (s.label)}
					<li class="card"><span class="muted">{s.label}</span><strong>{s.value}</strong></li>
				{/each}
			</ul>
		</div>

		{#if lensOpen && from}
			{#await import('#lib/rules/RuleLens.svelte') then { default: RuleLens }}
				<RuleLens {metric} start={from} {end} ondraft={(d) => (draft = d)} />
			{/await}
		{/if}
	</div>

	<section class="card values" aria-labelledby="values-h">
		<div class="values-head">
			<h2 id="values-h">Values</h2>
			<span class="muted">Newest first</span>
		</div>
		<div>
			{#if trend}
				<table>
					<thead>
						<tr><th scope="col">{trend.grain === 'week' ? 'Week of' : 'Month of'}</th><th scope="col" class="num">Mean</th><th scope="col" class="num">Min</th><th scope="col" class="num">Max</th><th scope="col" class="num">Days with data</th></tr>
					</thead>
					<tbody>
						{#each trend.buckets.toReversed().slice(0, rows) as b (b.start_date)}
							<tr>
								<th scope="row">{day(b.start_date)}</th>
								<td class="num">{fmt(b.mean)}</td>
								<td class="num">{fmt(b.min)}</td>
								<td class="num">{fmt(b.max)}</td>
								<td class="num">{b.n} / {b.days}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{:else}
				<table>
					<thead>
						<tr><th scope="col">Date</th><th scope="col" class="num">Value</th><th scope="col">Status</th><th scope="col" class="wide">Source</th><th scope="col" class="wide">Why</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr>
					</thead>
					<tbody>
						{#each dates.toReversed().slice(0, rows) as d (d)}
							{@const r = daily?.[d]}
							<tr class:current={selected === d}>
								<th scope="row">{day(d)}</th>
								<td class="num">
									{#if r && r.status !== 'no_data'}{formatValue(r.value, r.unit)}{:else}–{/if}
								</td>
								<td><ResultStatus status={r?.status ?? 'no_data'} partial={r?.partial} /></td>
								<td class="wide">{r?.selected ?? r?.inputs?.find((i) => i.selected)?.group ?? '–'}</td>
								<td class="wide">{#if r}<ExplainPopover text={r.explanation} warnings={warningCodes(r)} label="Explain" />{/if}</td>
								<td class="row-actions">
									<button class="btn link" type="button" onclick={() => (selected = d)}>Details<span class="visually-hidden"> of {d}</span></button>
									<a href="/explore/{encodeURIComponent(metric)}/day/{d}">All sources</a>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</div>
		{#if (trend?.buckets.length ?? dates.length) > rows}
			<button class="btn ghost sm more" type="button" onclick={() => (rows = Infinity)}>Show all {trend?.buckets.length ?? dates.length}</button>
		{/if}
	</section>
{/if}

<style>
	.crumbs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-4);
	}
	.title h1 {
		display: inline;
		margin-right: var(--space-3);
	}
	.code {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--color-text-faint);
	}
	.latest {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-3);
		margin: var(--space-3) 0 0;
	}
	.latest strong {
		font-size: var(--text-display);
		font-weight: 600;
		line-height: 1;
		letter-spacing: var(--tracking-tight);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
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
	.rule {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-2) var(--space-3);
		margin: var(--space-4) 0 0;
		font-size: var(--text-sm);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.rule a {
		margin-left: auto;
	}
	.controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2) var(--space-4);
		margin: var(--space-4) 0;
		font-size: var(--text-sm);
	}
	.controls label {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-height: var(--control-h-sm);
	}
	.span {
		flex: 1 1 12rem;
	}
	.layout {
		display: grid;
		gap: var(--space-4);
		margin-bottom: var(--space-4);
	}
	@media (min-width: 64rem) {
		.layout.with-lens {
			grid-template-columns: minmax(0, 1fr) 22rem;
			align-items: start;
		}
	}
	.main {
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	.chart {
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4);
		min-width: 0;
	}
	.sub {
		margin: 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.small {
		margin: 0;
		font-size: var(--text-sm);
	}
	.stats {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr));
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.stats li {
		display: grid;
		gap: var(--space-1);
		padding: var(--space-3) var(--space-4);
		font-size: var(--text-xs);
	}
	.stats strong {
		font-size: var(--text-lg);
	}
	.values {
		padding: 0;
	}
	.values-head {
		display: flex;
		align-items: baseline;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
		font-size: var(--text-sm);
		border-bottom: 1px solid var(--color-border);
	}
	.values-head h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2) var(--space-4);
		text-align: left;
		white-space: nowrap;
		border-top: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
		border-top: 0;
	}
	tbody th {
		font-weight: 500;
	}
	.num {
		text-align: right;
	}
	tr.current {
		background: var(--color-selected);
	}
	.row-actions {
		display: flex;
		gap: var(--space-3);
		align-items: center;
	}
	@media (max-width: 40rem) {
		.wide {
			display: none;
		}
		th,
		td {
			padding: var(--space-2);
			white-space: normal;
		}
		.row-actions {
			flex-direction: column;
			align-items: flex-start;
			gap: var(--space-1);
		}
	}
	.more {
		margin: var(--space-2) var(--space-4) var(--space-3);
	}
</style>
