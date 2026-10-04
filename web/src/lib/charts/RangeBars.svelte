<!--
	Floating bars from a start to an end value per window, e.g. bed time to wake time per night on a
	clock axis. The axis runs downwards (earlier at the top), with the mean of each end as a dashed
	line. The picked window is outlined. Values are numbers (clock hours past 24 for after
	midnight); `format` writes them (clockText for hours).
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { DAY, extent, formatInstant, formatNumber } from './scale.ts';
	import type { TableData, Tip, TipAction } from './types.ts';

	let {
		xs,
		lo,
		hi,
		label,
		loLabel = 'Start',
		hiLabel = 'End',
		picked = -1,
		means = true,
		timezone,
		height = 300,
		tickStep = 2,
		format = (v: number) => formatNumber(v),
		actions,
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
		/** Draw the mean of the starts and of the ends. */
		means?: boolean;
		timezone?: string;
		height?: number;
		/** Y tick every this many units (2 clock hours). */
		tickStep?: number;
		format?: (v: number) => string;
		actions?: TipAction[];
		onselect?: (i: number) => void;
	} = $props();

	// Values are negated so that earlier values sit higher on the axis.
	const has = (i: number) => lo[i] != null && hi[i] != null;
	const step = $derived(xs.length > 1 ? Math.min(...xs.slice(1).map((t, i) => t - xs[i])) : DAY);
	const x = $derived<[number, number]>(xs.length ? [xs[0] - step / 2, xs[xs.length - 1] + step / 2] : [0, 1]);
	const y = $derived(extent([...lo, ...hi].map((v) => (v == null ? null : -v)), 0.08));
	const yTicks = $derived.by(() => {
		const out: number[] = [];
		for (let v = Math.ceil(-y[1] / tickStep) * tickStep; v <= -y[0]; v += tickStep) out.push(-v);
		return out;
	});
	const mean = (vs: (number | null)[]) => {
		const ok = vs.filter((v): v is number => v != null);
		return ok.length ? ok.reduce((a, b) => a + b, 0) / ok.length : null;
	};
	const lines = $derived(means ? [mean(lo), mean(hi)] : []);
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

<ChartFrame {label} {xs} {x} {y} {timezone} {height} {tip} {table} {actions} {onselect} crosshair={false} yFormat={(v) => format(-v)} {yTicks}>
	{#snippet legend()}
		<span class="key"><span class="swatch"></span>{loLabel} to {hiLabel.toLowerCase()}</span>
		{#if means}<span class="key"><svg width="16" height="8" aria-hidden="true"><line x1="0" x2="16" y1="4" y2="4" /></svg>Mean {loLabel.toLowerCase()} and {hiLabel.toLowerCase()}</span>{/if}
	{/snippet}
	{#snippet marks(f)}
		{@const w = Math.max(2, (f.sx(x[0] + step) - f.sx(x[0])) * 0.6)}
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
		{#each lines as m, k (k)}
			{#if m != null}<line class="mean" x1={f.left} x2={f.right} y1={f.sy(-m)} y2={f.sy(-m)} />{/if}
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
	.mean {
		stroke: var(--color-text-muted);
		stroke-width: 1.5;
		stroke-dasharray: 4 4;
	}
	.key {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.key line {
		stroke: var(--color-text-muted);
		stroke-width: 2;
		stroke-dasharray: 3 3;
	}
	.swatch {
		width: 0.5rem;
		height: 0.75rem;
		background: var(--metric, var(--stage-light));
		border-radius: 2px;
	}
</style>
