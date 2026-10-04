<!--
	Lines over time: one or more series (sources keep their colour and differ by dash too),
	an optional range band and baseline, status markers, a ghost (draft) series, and a step
	variant with readings for "latest" metrics. Dense series are reduced per pixel column.
	The first series drives the pointer and keyboard; the tooltip shows every series there.
-->
<script lang="ts">
	import { dataStatus, ringPath } from '../ui/status.ts';
	import { sourceClass } from '../ui/source.ts';
	import ChartFrame from './ChartFrame.svelte';
	import { bandPath, extent, formatInstant, formatNumber, linePath, nearest } from './scale.ts';
	import type { Series, TableData, Tip } from './types.ts';

	let {
		series,
		label,
		unit = '',
		timezone,
		height,
		step = false,
		band,
		baseline,
		zoom = true,
		withTime = true,
		onselect
	}: {
		series: Series[];
		label: string;
		unit?: string;
		timezone?: string;
		height?: number;
		/** Step line with a dot per reading (each value holds until the next). */
		step?: boolean;
		band?: { xs: number[]; lo: (number | null)[]; hi: (number | null)[]; label: string };
		baseline?: { value: number; label: string };
		zoom?: boolean;
		/** Tooltip and table show the clock time (false for daily windows). */
		withTime?: boolean;
		onselect?: (i: number) => void;
	} = $props();

	// Sources differ by dash as well as colour; dense series (more points than pixels) stay solid.
	const dashes = ['', '6 4', '1.5 3', '10 3 2 3', '4 4'];
	const markerLimit = 400;

	const anchors = $derived(series[0]?.xs ?? []);
	const x = $derived(extent([...series.flatMap((s) => [s.xs[0], s.xs.at(-1)]), band?.xs[0], band?.xs.at(-1)]));
	const y = $derived(
		extent([...series.flatMap((s) => s.ys), ...(band ? [...band.lo, ...band.hi] : []), baseline?.value], 0.08)
	);
	const fmt = (v: number | null | undefined) => (v == null ? '–' : `${formatNumber(v)}${unit ? ` ${unit}` : ''}`);

	function valueAt(s: Series, t: number): { v: number | null; j: number } {
		const j = nearest(s.xs, t);
		const tol = (x[1] - x[0]) / 400;
		return j >= 0 && Math.abs(s.xs[j] - t) <= tol ? { v: s.ys[j], j } : { v: null, j: -1 };
	}

	function tip(i: number): Tip {
		const t = anchors[i];
		return {
			title: formatInstant(t, timezone, withTime),
			rows: series.map((s) => {
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

	const colour = (s: Series) => (s.source ? sourceClass(s.source) : 'resolved');
</script>

<ChartFrame {label} xs={anchors} {x} {y} {timezone} {height} {zoom} {tip} {table} {onselect}>
	{#snippet legend()}
		{#if series.length > 1 || band || baseline}
			{#each series as s, k (k)}
				<span class={['key', colour(s), s.style]}>
					<svg width="16" height="8" aria-hidden="true"><line x1="0" x2="16" y1="4" y2="4" stroke-dasharray={s.style === 'ghost' ? '4 3' : dashes[k % dashes.length]} /></svg>
					{s.label}
				</span>
			{/each}
			{#if band}<span class="key"><span class="band-key"></span>{band.label}</span>{/if}
			{#if baseline}<span class="key"><svg width="16" height="8" aria-hidden="true"><line class="baseline" x1="0" x2="16" y1="4" y2="4" /></svg>{baseline.label}</span>{/if}
		{/if}
	{/snippet}
	{#snippet marks(f)}
		{#if band}<path class="band" d={bandPath(band.xs, band.lo, band.hi, f.sx, f.sy)} />{/if}
		{#if baseline}<line class="baseline" x1={f.left} x2={f.right} y1={f.sy(baseline.value)} y2={f.sy(baseline.value)} />{/if}
		{#each series as s, k (k)}
			<g class={colour(s)}>
				{#if s.style !== 'dots'}
					<path
						class={['line', s.style, k > 0 && 'secondary']}
						d={linePath(s.xs, s.ys, f.sx, f.sy, step)}
						stroke-dasharray={s.style === 'ghost' ? '6 4' : (s.xs.length < f.right - f.left && dashes[k % dashes.length]) || undefined}
					/>
				{/if}
				{#if (step || s.style === 'dots') && s.xs.length <= markerLimit}
					{#each s.xs as t, j (j)}
						{#if s.ys[j] != null}<circle class="dot" cx={f.sx(t)} cy={f.sy(s.ys[j] ?? 0)} r="3.5" />{/if}
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
		{#if f.active >= 0 && series[0]}
			{@const v = series[0].ys[f.active]}
			{#if v != null}<circle class="focus" cx={f.sx(anchors[f.active])} cy={f.sy(v)} r="5" />{/if}
		{/if}
	{/snippet}
</ChartFrame>

<style>
	.resolved {
		--src: var(--color-accent);
	}
	.line {
		fill: none;
		stroke: var(--src);
		stroke-width: 2.25;
		stroke-linejoin: round;
		stroke-linecap: round;
	}
	.line.secondary {
		stroke-width: 1.5;
		stroke-opacity: 0.85;
	}
	.line.ghost {
		stroke: var(--color-text);
		stroke-width: 1.75;
		stroke-opacity: 0.9;
	}
	.dot {
		fill: var(--src);
		stroke: var(--color-surface);
		stroke-width: 1.5;
	}
	.band {
		fill: var(--chart-band);
	}
	.baseline {
		stroke: var(--color-text-muted);
		stroke-dasharray: 3 3;
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
		fill: var(--color-accent);
		stroke: var(--color-surface);
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
		background: var(--chart-band);
		border-radius: 2px;
	}
</style>
