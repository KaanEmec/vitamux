<!--
	Lines over time on LayerChart: one or more series, an optional area under the first, a range
	band and a labelled baseline (the mean line), status markers for non-direct points, a ghost
	(draft) series, dots, and a step variant with readings for "latest" metrics. Gaps stay gaps,
	shaded as "No data", never zero. Source overlays are the other series with a `source`: they take
	their source colour and a dash, so colour is never the only cue (the page decides which are
	shown). Dense series are reduced per column before they are drawn. The first series drives the
	pointer, keyboard and the tooltip's value, status and providers; the others are listed under it.
-->
<script lang="ts">
	import { Area, Spline } from 'layerchart/svg';
	import { dataStatus, ringPath } from '../ui/status.ts';
	import { sourceClass } from '../ui/source.ts';
	import ChartFrame from './ChartFrame.svelte';
	import { decimate, extent, formatInstant, formatNumber, nearest, stepRows, type Domain, type Row } from './scale.ts';
	import type { Series, TableData, Tip, TipAction } from './types.ts';

	let {
		series,
		label,
		unit = '',
		timezone,
		height,
		step = false,
		area = false,
		band,
		baseline,
		zoom = true,
		view = $bindable(null),
		withTime = true,
		gaps = true,
		actions,
		onselect
	}: {
		series: Series[];
		label: string;
		unit?: string;
		timezone?: string;
		height?: number;
		/** Step line with a dot per reading (each value holds until the next). */
		step?: boolean;
		/** Fill under the first series, fading down. */
		area?: boolean;
		band?: { xs: number[]; lo: (number | null)[]; hi: (number | null)[]; label: string };
		/** A labelled horizontal line, e.g. the 30-day mean. */
		baseline?: { value: number; label: string };
		zoom?: boolean;
		/** Zoomed x domain, shared with a BrushNavigator. */
		view?: Domain | null;
		/** Tooltip and table show the clock time (false for daily windows). */
		withTime?: boolean;
		/** Shade the first series' gaps as "No data". */
		gaps?: boolean;
		actions?: TipAction[];
		onselect?: (i: number) => void;
	} = $props();

	// Sources differ by dash as well as colour; dense series (more points than pixels) stay solid.
	const dashes = ['', '5 4', '1.5 3', '10 3 2 3', '4 4'];
	const markerLimit = 400;
	const uid = $props.id();

	const anchors = $derived(series[0]?.xs ?? []);
	const x = $derived(extent([...series.flatMap((s) => [s.xs[0], s.xs.at(-1)]), band?.xs[0], band?.xs.at(-1)]));
	const y = $derived(extent([...series.flatMap((s) => s.ys), ...(band ? [...band.lo, ...band.hi] : []), baseline?.value], 0.08));
	const shown = $derived(view ?? x);
	const rows = $derived(
		series.map((s) => {
			const r = decimate(s.xs, s.ys, shown);
			return step ? stepRows(r) : r;
		})
	);
	const bandRows = $derived(band?.xs.map((t, i) => ({ x: t, lo: band.lo[i], hi: band.hi[i] })) ?? []);
	/** Runs of missing values in the first series, as [from, to] in ms. */
	const holes = $derived.by(() => {
		const s = series[0];
		const out: [number, number][] = [];
		if (!gaps || !s) return out;
		for (let i = 0; i < s.xs.length; i++) {
			if (s.ys[i] != null) continue;
			let j = i;
			while (j + 1 < s.xs.length && s.ys[j + 1] == null) j++;
			out.push([s.xs[Math.max(i - 1, 0)], s.xs[Math.min(j + 1, s.xs.length - 1)]]);
			i = j;
		}
		return out;
	});
	const fmt = (v: number | null | undefined) => (v == null ? '–' : `${formatNumber(v)}${unit ? ` ${unit}` : ''}`);
	const defined = (d: Row) => d.y != null;
	const colour = (s: Series) => (s.source ? sourceClass(s.source) : 'resolved');
	const dash = (s: Series, k: number, px = Infinity) =>
		s.style === 'ghost' ? '6 4' : (k > 0 && s.xs.length < px && dashes[k % dashes.length]) || undefined;

	function valueAt(s: Series, t: number): { v: number | null; j: number } {
		const j = nearest(s.xs, t);
		const tol = (x[1] - x[0]) / 400;
		return j >= 0 && Math.abs(s.xs[j] - t) <= tol ? { v: s.ys[j], j } : { v: null, j: -1 };
	}

	function tip(i: number): Tip {
		const t = anchors[i];
		const [first, ...rest] = series;
		const v = first.ys[i];
		return {
			title: formatInstant(t, timezone, withTime),
			lead: {
				value: v == null ? 'No data' : formatNumber(v),
				unit: v == null ? undefined : unit,
				status: first.status?.[i] ?? undefined,
				providers: first.providers?.[i] ?? (first.source ? [first.source] : undefined)
			},
			rows: rest.map((s) => {
				const { v, j } = valueAt(s, t);
				return { label: s.label, value: fmt(v), source: s.source, status: j >= 0 ? (s.status?.[j] ?? undefined) : undefined };
			})
		};
	}

	function table(): TableData {
		const byTime: Record<number, (number | null)[]> = {};
		series.forEach((s, k) =>
			s.xs.forEach((t, j) => {
				byTime[t] ??= series.map(() => null);
				byTime[t][k] = s.ys[j];
			})
		);
		return {
			columns: [withTime ? 'Time' : 'Date', ...series.map((s) => `${s.label}${unit ? ` (${unit})` : ''}`)],
			rows: Object.keys(byTime)
				.map(Number)
				.sort((a, b) => b - a)
				.map((t) => [formatInstant(t, timezone, withTime), ...byTime[t].map((v) => (v == null ? '–' : formatNumber(v)))])
		};
	}
</script>

<ChartFrame {label} xs={anchors} {x} {y} {timezone} {height} {zoom} bind:view {tip} {table} {actions} {onselect}>
	{#snippet legend()}
		{#if series.length > 1 || band || baseline}
			{#each series as s, k (k)}
				<span class={['key', colour(s), s.style]}>
					<svg width="16" height="8" aria-hidden="true"><line x1="0" x2="16" y1="4" y2="4" stroke-dasharray={dash(s, k)} /></svg>
					{s.label}
				</span>
			{/each}
			{#if band}<span class="key"><span class="band-key"></span>{band.label}</span>{/if}
			{#if baseline}<span class="key"><svg width="16" height="8" aria-hidden="true"><line class="baseline" x1="0" x2="16" y1="4" y2="4" /></svg>{baseline.label}</span>{/if}
		{/if}
	{/snippet}
	{#snippet marks(f)}
		<g class="ts">
			{#each holes as [a, b], k (k)}
				{@const w = f.sx(b) - f.sx(a)}
				{#if w >= 2}
					<rect class="hole" x={f.sx(a)} y={f.top} width={w} height={f.bottom - f.top} />
					{#if w >= 56}<text class="hole-label" x={f.sx(a) + 6} y={f.top + 14}>No data</text>{/if}
				{/if}
			{/each}
			{#if band}<Area data={bandRows} x="x" y0="lo" y1="hi" defined={(d: { lo: number | null; hi: number | null }) => d.lo != null && d.hi != null} class="band" />{/if}
			{#each series as s, k (k)}
				<g class={colour(s)}>
					{#if k === 0 && area && s.style !== 'dots'}
						<defs>
							<linearGradient id="{uid}-fill" x1="0" x2="0" y1="0" y2="1">
								<stop offset="0" class="stop-top" />
								<stop offset="1" class="stop-bottom" />
							</linearGradient>
						</defs>
						<Area data={rows[k]} x="x" y0={() => y[0]} y1="y" {defined} fill="url(#{uid}-fill)" class="area" />
					{/if}
					{#if s.style !== 'dots'}
						<Spline data={rows[k]} x="x" y="y" {defined} class={['line', s.style, k > 0 && 'secondary'].filter(Boolean).join(' ')} stroke-dasharray={dash(s, k, f.right - f.left)} />
					{/if}
					{#if (step || s.style === 'dots') && s.xs.length <= markerLimit}
						{#each s.xs as t, j (j)}
							{@const v = s.ys[j]}
							{#if v != null}<circle class="dot" cx={f.sx(t)} cy={f.sy(v)} r="3.5" />{/if}
						{/each}
					{/if}
					{#if s.status && s.xs.length <= markerLimit}
						{#each s.xs as t, j (j)}
							{@const st = s.status[j]}
							{#if st && st !== 'direct'}
								<g class={['marker', st]} transform="translate({f.sx(t) - 6} {(st === 'no_data' ? f.bottom - 6 : f.sy(s.ys[j] ?? 0)) - 6})">
									{#if dataStatus[st].ring}<path class="ring" d={ringPath} />{/if}
									{#if dataStatus[st].shape}<path d={dataStatus[st].shape} />{/if}
								</g>
							{/if}
						{/each}
					{/if}
				</g>
			{/each}
			{#if baseline}
				<line class="baseline" x1={f.left} x2={f.right} y1={f.sy(baseline.value)} y2={f.sy(baseline.value)} />
				<text class="baseline-label" x={f.right - 4} y={f.sy(baseline.value) - 5} text-anchor="end">{baseline.label}</text>
			{/if}
			{#if f.active >= 0 && series[0]}
				{@const v = series[0].ys[f.active]}
				{#if v != null}<circle class="focus" cx={f.sx(anchors[f.active])} cy={f.sy(v)} r="5" />{/if}
			{/if}
		</g>
	{/snippet}
</ChartFrame>

<style>
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.resolved,
	.ts :global(.resolved) {
		--src: var(--metric, var(--color-accent));
	}
	.ts :global(.line) {
		fill: none;
		stroke: var(--src);
		stroke-width: 2.25;
		stroke-linejoin: round;
		stroke-linecap: round;
	}
	.ts :global(.line.secondary) {
		stroke-width: 1.5;
		stroke-opacity: 0.8;
	}
	.ts :global(.line.ghost) {
		stroke: var(--color-text);
		stroke-width: 1.75;
		stroke-opacity: 0.9;
	}
	.ts :global(.area) {
		stroke: none;
	}
	.stop-top {
		stop-color: var(--src);
		stop-opacity: 0.3;
	}
	.stop-bottom {
		stop-color: var(--src);
		stop-opacity: 0;
	}
	.ts :global(.band) {
		fill: color-mix(in srgb, var(--metric, var(--color-accent)) 12%, transparent);
		stroke: none;
	}
	.hole {
		fill: var(--chart-muted);
		fill-opacity: 0.35;
	}
	.hole-label,
	.baseline-label {
		font-size: var(--text-2xs);
		fill: var(--color-text-muted);
	}
	.dot {
		fill: var(--src);
		stroke: var(--color-surface);
		stroke-width: 1.5;
	}
	.baseline {
		stroke: var(--color-text-muted);
		stroke-width: 1.5;
		stroke-dasharray: 4 4;
		stroke-opacity: 0.8;
	}
	.marker path {
		fill: currentColor;
		stroke: var(--color-surface);
		stroke-width: 1.2;
	}
	.marker .ring {
		fill: var(--color-surface);
		stroke: currentColor;
		stroke-width: 1.4;
	}
	.fallback {
		color: var(--status-fallback);
	}
	.calculated {
		color: var(--status-calculated);
	}
	.overridden {
		color: var(--status-overridden);
	}
	.partial {
		color: var(--status-partial);
	}
	.no_data {
		color: var(--status-none);
	}
	.focus {
		fill: var(--src, var(--metric, var(--color-accent)));
		stroke: var(--color-text);
		stroke-width: 2;
	}
	.key {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.key line {
		stroke: var(--src, var(--color-text-muted));
		stroke-width: 2;
	}
	.key.ghost line {
		stroke: var(--color-text);
	}
	.band-key {
		width: 1rem;
		height: 0.5rem;
		background: color-mix(in srgb, var(--metric, var(--color-accent)) 12%, transparent);
		border-radius: 2px;
	}
</style>
