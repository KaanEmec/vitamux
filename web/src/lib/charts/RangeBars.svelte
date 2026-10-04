<!--
	Floating bars from a start to an end value per window, e.g. bed time to wake time per night on a
	clock axis. The axis runs downwards (earlier at the top), with shaded bands (the middle half of
	each end). The picked window is outlined. Values are numbers (clock hours past 24 for after
	midnight); `format` writes them (clockText for hours).
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { DAY, extent, formatInstant, formatNumber } from './scale.ts';
	import type { TableData, Tip } from './types.ts';

	let {
		xs,
		lo,
		hi,
		label,
		loLabel = 'Start',
		hiLabel = 'End',
		picked = -1,
		bands = [],
		bandLabel = 'Middle half of the windows',
		timezone,
		format = (v: number) => formatNumber(v),
		onselect
	}: {
		/** Window starts (epoch ms), ascending. */
		xs: number[];
		lo: (number | null)[];
		hi: (number | null)[];
		label: string;
		loLabel?: string;
		hiLabel?: string;
		/** Index of the outlined window. */
		picked?: number;
		/** Shaded value ranges (e.g. the middle half of the starts and of the ends). */
		bands?: [number, number][];
		bandLabel?: string;
		timezone?: string;
		format?: (v: number) => string;
		onselect?: (i: number) => void;
	} = $props();

	// Values are negated so that earlier values sit higher on the axis; a y tick every 2 units (clock hours).
	const tickStep = 2;
	const has = (i: number) => lo[i] != null && hi[i] != null;
	const step = $derived(xs.length > 1 ? Math.min(...xs.slice(1).map((t, i) => t - xs[i])) : DAY);
	const x = $derived<[number, number]>(xs.length ? [xs[0] - step / 2, xs[xs.length - 1] + step / 2] : [0, 1]);
	const y = $derived(extent([...lo, ...hi].map((v) => (v == null ? null : -v)), 0.08));
	const yTicks = $derived.by(() => {
		const out: number[] = [];
		for (let v = Math.ceil(-y[1] / tickStep) * tickStep; v <= -y[0]; v += tickStep) out.push(-v);
		return out;
	});
	const when = (i: number) => formatInstant(xs[i], timezone, step < DAY);

	const tip = (i: number): Tip => ({
		title: when(i),
		lead: has(i) ? { value: `${format(lo[i] ?? 0)} – ${format(hi[i] ?? 0)}` } : { value: 'No data' },
		rows: []
	});
	const table = (): TableData => ({
		columns: ['Window', loLabel, hiLabel],
		rows: xs.map((_, i) => [when(i), has(i) ? format(lo[i] ?? 0) : '–', has(i) ? format(hi[i] ?? 0) : '–']).reverse()
	});
</script>

<ChartFrame {label} {xs} {x} {y} {timezone} height={300} {tip} {table} {onselect} crosshair={false} yFormat={(v) => format(-v)} {yTicks}>
	{#snippet legend()}
		<span class="key"><span class="swatch"></span>{loLabel} to {hiLabel.toLowerCase()}</span>
		{#if bands.length}<span class="key"><span class="band-key"></span>{bandLabel}</span>{/if}
	{/snippet}
	{#snippet marks(f)}
		{@const w = Math.max(2, (f.sx(x[0] + step) - f.sx(x[0])) * 0.6)}
		{#each bands as [a, b], k (k)}
			<rect class="band" x={f.left} width={f.right - f.left} y={f.sy(-b)} height={Math.max(0, f.sy(-a) - f.sy(-b))} />
		{/each}
		{#each xs as t, i (t)}
			{#if has(i)}
				<rect
					class={['bar', f.active >= 0 && f.active !== i && 'dim', i === picked && 'picked']}
					x={f.sx(t) - w / 2}
					y={f.sy(-(lo[i] ?? 0))}
					width={w}
					height={Math.max(2, f.sy(-(hi[i] ?? 0)) - f.sy(-(lo[i] ?? 0)))}
					rx={Math.min(4, w / 2)}
				/>
			{/if}
		{/each}
	{/snippet}
</ChartFrame>

<style>
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.bar {
		fill: var(--metric, var(--stage-light));
		fill-opacity: 0.85;
	}
	.bar.dim {
		fill-opacity: 0.5;
	}
	.bar.picked {
		stroke: var(--color-text);
		stroke-width: 1.5;
	}
	.band {
		fill: color-mix(in srgb, var(--metric, var(--stage-light)) 10%, transparent);
		stroke: color-mix(in srgb, var(--metric, var(--stage-light)) 40%, transparent);
		stroke-dasharray: 4 4;
	}
	.band-key {
		width: 1rem;
		height: 0.5rem;
		background: color-mix(in srgb, var(--metric, var(--stage-light)) 14%, transparent);
		border: 1px dashed color-mix(in srgb, var(--metric, var(--stage-light)) 50%, transparent);
		border-radius: 2px;
	}
	.key {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.swatch {
		width: 0.5rem;
		height: 0.75rem;
		background: var(--metric, var(--stage-light));
		border-radius: 2px;
	}
</style>
