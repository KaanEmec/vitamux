<!--
	The Day view of a metric with `intraday` (J26.3): one local day through the catalogue's ladder.
	The 24-hour span reads the default bucket; dragging on the chart or the navigator picks a finer
	step for the visible span, down to the finest or raw rows, and never draws a source finer than
	its native spacing (lib/explore/intraday.ts). Intensive metrics are a line with each bucket's
	min–max band, additive ones bars (a line once sources are overlaid). Under the marks: the
	resolved sleep episode's night, workout bands and a now line on today; the source strip is a
	toggle. A bucket opens its value, span, source and a link to the day's explanation; at raw zoom
	it lists the readings in it with their time, device and origin.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import ResultStatus from '../components/ResultStatus.svelte';
	import type { Frame } from '../charts/ChartFrame.svelte';
	import { formatClock, formatNumber, type Domain } from '../charts/scale.ts';
	import type { Series } from '../charts/types.ts';
	import { providerLabel } from '../connections/connections.ts';
	import { addDays, metricLabel, today } from '../data/format.ts';
	import { groupLabel } from '../rules/rule.ts';
	import { zonedInstant } from '../settings/tz.ts';
	import Skeleton from '../ui/Skeleton.svelte';
	import { displayStatus } from '../ui/status.ts';
	import { dayLabel } from '../views/format.ts';
	import { ladder, loadLayer, loadSpan, stepFor, stepWord, type Layer } from './intraday.ts';
	import { num, sourceLabel } from './series.ts';

	let {
		metric,
		meta,
		date,
		shown,
		band = true,
		strip = false,
		drill = true
	}: {
		metric: string;
		meta: Schemas['Metric'] & { intraday: Schemas['Intraday'] };
		date: string;
		/** Providers whose own series are drawn; all of them when absent. */
		shown?: string[];
		/** The min–max band of each bucket (intensive metrics). */
		band?: boolean;
		/** The source behind each bucket, as a strip under the chart. */
		strip?: boolean;
		/** The bucket panel links to the all-sources day view. */
		drill?: boolean;
	} = $props();

	type Night = { start: number; end: number };
	type Workout = Night & { sport: string };

	const steps = $derived(ladder(meta.intraday));
	const additive = $derived(meta.aggregation === 'additive');
	const label = $derived(metricLabel(metric));

	let tz = $state('UTC');
	let day = $state<Domain | null>(null);
	let nights = $state<Night[]>([]);
	let workouts = $state<Workout[]>([]);
	let base = $state<Layer | null>(null);
	let detail = $state<Layer | null>(null);
	let problem = $state<Problem | null>(null);
	let view = $state<Domain | null>(null);
	let picked = $state<{ layer: Layer; i: number } | null>(null);

	const span = (s: { start: string; end: string }) => ({ start: Date.parse(s.start), end: Date.parse(s.end) });

	// The day's bounds in the owner's timezone (from the sleep and workout answers), its overlays and the 24-hour layer.
	let gen = 0;
	$effect(() => {
		const [m, d, st] = [metric, date, steps];
		const mine = ++gen;
		base = detail = picked = view = day = null;
		problem = null;
		void (async () => {
			const [sleep, sport] = await Promise.all([
				api.GET('/api/v1/resolved/sleep', { params: { query: { start_date: addDays(d, -1), end_date: addDays(d, 1) } } }),
				api.GET('/api/v1/resolved/workouts', { params: { query: { start_date: d, end_date: d } } })
			]);
			if (mine !== gen) return;
			tz = sleep.data?.timezone || sport.data?.timezone || 'UTC';
			const bounds: Domain = [Date.parse(zonedInstant(`${d}T00:00`, tz) ?? ''), Date.parse(zonedInstant(`${addDays(d, 1)}T00:00`, tz) ?? '')];
			nights = (sleep.data?.nights ?? []).flatMap((n) => (n.episode ? [span(n.episode)] : [])).filter((n) => n.end > bounds[0] && n.start < bounds[1]);
			workouts = (sport.data?.workouts ?? []).map((w) => ({ ...span(w), sport: w.sport }));
			day = bounds;
			const layer = await loadLayer(m, st, st[0][0], ...bounds);
			if (mine !== gen) return;
			base = layer;
			problem = layer.problem;
		})();
	});

	const step = $derived(view ? stepFor(steps, view[1] - view[0]) : steps[0][0]);

	// A zoom finer than the 24-hour bucket loads the visible span at its step (after the drag settles).
	$effect(() => {
		const [s, v, d, m, st] = [step, view, day, metric, steps];
		if (!v || !d || s === st[0][0]) return void (detail = null);
		if (detail && detail.step === s && detail.from <= v[0] && detail.to >= v[1]) return;
		const t = setTimeout(async () => {
			const layer = await loadLayer(m, st, s, ...loadSpan(s, v, d));
			if (view === v) {
				detail = layer;
				problem = layer.problem;
			}
		}, 250);
		return () => clearTimeout(t);
	});

	/** The finer layer while it covers the view, else the day's. */
	const layer = $derived(detail && view && detail.from <= view[0] && detail.to >= view[1] ? detail : base);
	const xs = $derived(layer?.points.map((p) => Date.parse(p.start ?? p.end)) ?? []);
	const resolved = $derived.by((): Series | null => {
		if (!layer) return null;
		return {
			label: `Resolved, ${stepWord(layer.bucket)}`,
			xs,
			ys: layer.points.map((p) => num(p.value, metric)),
			status: layer.points.map((p) => displayStatus(p.status, p.partial)),
			providers: layer.points.map((p) => p.providers ?? null)
		};
	});
	const overlays = $derived(
		(layer?.sources ?? [])
			.filter((s) => !shown || shown.includes(s.source.provider))
			.map((s): Series => {
				const pts = s.points.filter((p) => p.start);
				return {
					label: `${sourceLabel(s.source)}${s.step !== layer?.step ? `, ${stepWord(s.step)}` : ''}`,
					source: s.source.provider,
					xs: pts.map((p) => Date.parse(p.start ?? '')),
					ys: pts.map((p) => (s.step === 'raw' ? p.value : additive ? p.sum : p.mean) ?? null)
				};
			})
	);
	const bandData = $derived(
		band && !additive && layer ? { xs, lo: layer.points.map((p) => p.min ?? null), hi: layer.points.map((p) => p.max ?? null), label: 'Min–max per bucket' } : undefined
	);
	const bars = $derived(additive && !overlays.length);
	const hasValues = $derived(!!resolved?.ys.some((v) => v != null) || overlays.some((s) => s.ys.some((v) => v != null)));
	const chartLabel = $derived(`${label} on ${dayLabel(date)}, ${layer ? stepWord(layer.step) : ''}`);
	const now = $derived(date === today() && day ? Date.now() : null);
	const clock = (t: number) => formatClock(t, tz);
	const range = (a: number, b: number) => `${clock(a)}–${clock(b)}`;

	const stripRow = $derived(
		layer ? [{ label: 'Source per bucket', days: layer.points.map((p) => (p.status === 'no_data' ? 0 : 1)), sources: layer.points.map((p) => p.providers?.[0] ?? null) }] : []
	);

	const point = $derived(picked ? picked.layer.points[picked.i] : null);
	const pointSpan = $derived(point ? [Date.parse(point.start ?? point.end), Date.parse(point.end)] : null);
	/** The raw readings in the picked bucket, at raw zoom. */
	const readings = $derived.by(() => {
		if (!picked || picked.layer.step !== 'raw' || !pointSpan) return [];
		const [a, b] = pointSpan;
		return picked.layer.sources
			.filter((s) => s.step === 'raw')
			.flatMap((s) =>
				s.points.flatMap((p) => {
					const t = Date.parse(p.start ?? '');
					return t >= a && t < b ? [{ t, value: p.value, source: s.source }] : [];
				})
			)
			.sort((x, y) => x.t - y.t);
	});
	const seconds = (t: number) => new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', timeZone: tz }).format(t);
	/** The picked bucket's span, to the second for 30-second buckets. */
	const spanLabel = $derived.by(() => {
		if (!pointSpan || !picked) return '';
		const f = picked.layer.bucket === '30s' ? seconds : clock;
		return `${f(pointSpan[0])}–${f(pointSpan[1])}`;
	});

	function select(i: number) {
		if (layer) picked = { layer, i };
	}
</script>

{#snippet marks(f: Frame)}
	{#each nights as n, k (k)}
		<rect class="night" x={f.sx(n.start)} y={f.top} width={Math.max(0, f.sx(n.end) - f.sx(n.start))} height={f.bottom - f.top} />
	{/each}
	{#each workouts as w, k (k)}
		<rect class="workout" x={f.sx(w.start)} y={f.top} width={Math.max(1, f.sx(w.end) - f.sx(w.start))} height={f.bottom - f.top} />
	{/each}
	{#if now}<line class="now" x1={f.sx(now)} x2={f.sx(now)} y1={f.top} y2={f.bottom} />{/if}
{/snippet}

<ProblemAlert {problem} />
{#if !layer}
	<Skeleton variant="chart" label="Loading the day" />
{:else if !hasValues}
	<p class="muted">No values on {dayLabel(date)}. Step to another day, or choose a longer range.</p>
{:else}
	{#if bars && resolved}
		{#await import('../charts/Bars.svelte')}
			<Skeleton variant="chart" label="Loading chart" />
		{:then { default: Bars }}
			<Bars
				{xs}
				stacks={[{ label: 'Resolved', ys: resolved.ys }]}
				status={resolved.status}
				providers={resolved.providers}
				label={chartLabel}
				unit={meta.unit}
				timezone={tz}
				height={320}
				zoom
				bind:view
				overlay={marks}
				onselect={select}
			/>
		{/await}
	{:else if resolved}
		{#await import('../charts/TimeSeries.svelte')}
			<Skeleton variant="chart" label="Loading chart" />
		{:then { default: TimeSeries }}
			<TimeSeries series={[resolved, ...overlays]} label={chartLabel} unit={meta.unit} timezone={tz} height={320} band={bandData} area={!additive} bind:view overlay={marks} onselect={select} />
		{/await}
	{/if}
	<ul class="overlays muted small" aria-label="Overlays">
		{#each nights as n, k (k)}<li><span class="key night"></span>Night {range(n.start, n.end)}</li>{/each}
		{#each workouts as w, k (k)}<li><span class="key workout"></span>{metricLabel(w.sport)} {range(w.start, w.end)}</li>{/each}
		{#if now}<li><span class="key now"></span>Now {clock(now)}</li>{/if}
	</ul>
	{#if strip}
		{#await import('../charts/CoverageStrip.svelte') then { default: CoverageStrip }}
			<CoverageStrip rows={stripRow} start={date} noun="buckets" cellLabel={(i) => clock(xs[i])} caption="{label}: the source behind each bucket's value" />
		{/await}
	{/if}
	{#if base && base.points.length > 14}
		{#await import('../charts/BrushNavigator.svelte') then { default: BrushNavigator }}
			<BrushNavigator xs={base.points.map((p) => Date.parse(p.start ?? p.end))} ys={base.points.map((p) => num(p.value, metric))} bind:view label="{label} over the day" timezone={tz} withTime />
		{/await}
	{/if}
{/if}

{#if point && pointSpan}
	<section class="card bucket" aria-label="Bucket {spanLabel}">
		<div class="head">
			<h3>{spanLabel} <span class="muted small">{stepWord(picked?.layer.bucket ?? '1m').replace(' buckets', ' bucket')}</span></h3>
			<button class="btn ghost sm" type="button" onclick={() => (picked = null)}>Close</button>
		</div>
		<p class="headline">
			<ResultStatus status={point.status} partial={point.partial} />
			<strong class="value">{point.status === 'no_data' ? '–' : `${formatNumber(num(point.value, metric) ?? 0)} ${meta.unit}`}</strong>
			{#if point.min != null && point.max != null}<span class="muted">range {formatNumber(point.min)}–{formatNumber(point.max)}</span>{/if}
		</p>
		<p class="muted small">
			{#if point.providers?.length}From {point.providers.map(providerLabel).join(', ')}{#if point.sources.length} ({point.sources.map(groupLabel).join(', ')}){/if}.{:else}No source.{/if}
			{#if point.n != null}{point.n} {point.n === 1 ? 'reading' : 'readings'}.{/if}
			{#if point.coverage != null}Coverage {Math.round(point.coverage * 100)}%.{/if}
			{#if point.warnings?.length}Warnings: {point.warnings.join(', ')}.{/if}
		</p>
		{#if readings.length}
			<table>
				<caption class="visually-hidden">Readings from {spanLabel}</caption>
				<thead><tr><th scope="col">Time</th><th scope="col">Value</th><th scope="col">Device</th><th scope="col">Origin</th></tr></thead>
				<tbody>
					{#each readings as r, k (k)}
						<tr>
							<td>{seconds(r.t)}</td>
							<td>{r.value == null ? '–' : `${formatNumber(r.value)} ${meta.unit}`}</td>
							<td>{providerLabel(r.source.provider)}{r.source.device ? ` · ${r.source.device.model ?? r.source.device.type ?? ''}` : ''}</td>
							<td>{r.source.origin?.name ?? r.source.origin?.key ?? '–'}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{/if}
		{#if drill}<a href="/explore/{encodeURIComponent(metric)}/day/{date}">Explanation and all sources of {dayLabel(date)}</a>{/if}
	</section>
{/if}

<style>
	.night {
		fill: var(--chart-muted);
		fill-opacity: 0.3;
	}
	.workout {
		fill: color-mix(in srgb, var(--metric, var(--color-accent)) 16%, transparent);
	}
	.now {
		stroke: var(--color-text);
		stroke-width: 1.5;
		stroke-dasharray: 2 3;
	}
	.overlays {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.overlays:empty {
		display: none;
	}
	.overlays li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.key {
		width: 0.75rem;
		height: 0.5rem;
		border-radius: 2px;
	}
	.key.night {
		background: color-mix(in srgb, var(--chart-muted) 30%, transparent);
	}
	.key.workout {
		background: color-mix(in srgb, var(--metric, var(--color-accent)) 16%, transparent);
	}
	.key.now {
		width: 0;
		height: 0.75rem;
		border-left: 1.5px dashed var(--color-text);
		border-radius: 0;
	}
	.small {
		font-size: var(--text-xs);
	}
	.bucket {
		display: grid;
		gap: var(--space-2);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	h3,
	p {
		margin: 0;
	}
	h3 {
		font-size: var(--text-md);
	}
	.headline {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-3);
	}
	.value {
		font-size: var(--text-xl);
	}
	table {
		width: 100%;
		font-size: var(--text-sm);
		border-collapse: collapse;
	}
	th,
	td {
		padding: var(--space-1) var(--space-2);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
	}
</style>
