<!--
	Bars per window (additive metrics: steps, energy) in the metric hue, or bars stacked by stage
	(sleep stages per night, `color` per stack, deep at the bottom). xs are window starts, evenly
	spaced; a missing window has no bar (never a zero bar). An optional labelled baseline (the
	mean line) is drawn over them. Period changes animate through the frame's domain motion.
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { DAY, extent, formatInstant, formatNumber } from './scale.ts';
	import type { StageColor } from './sleep.ts';
	import type { TableData, Tip, TipAction } from './types.ts';
	import type { DataStatus } from '../ui/status.ts';

	let {
		xs,
		stacks,
		label,
		unit = '',
		timezone,
		height,
		baseline,
		status,
		providers,
		format = (v: number) => `${formatNumber(v)}${unit ? ` ${unit}` : ''}`,
		picked = -1,
		actions,
		onselect
	}: {
		xs: number[];
		stacks: { label: string; ys: (number | null)[]; color?: StageColor | 'accent' | 'info' }[];
		label: string;
		unit?: string;
		timezone?: string;
		height?: number;
		baseline?: { value: number; label: string };
		/** Per window, for the tooltip of a single stack. */
		status?: (DataStatus | null)[];
		/** Per window, the providers behind the value (GET /resolved/series `providers`), for the tooltip. */
		providers?: (string[] | null)[];
		format?: (v: number) => string;
		/** Index of the outlined window. */
		picked?: number;
		actions?: TipAction[];
		onselect?: (i: number) => void;
	} = $props();

	const stepMs = $derived(xs.length > 1 ? Math.min(...xs.slice(1).map((t, i) => t - xs[i])) : DAY);
	const totals = $derived(xs.map((_, i) => (stacks.some((s) => s.ys[i] != null) ? stacks.reduce((n, s) => n + (s.ys[i] ?? 0), 0) : null)));
	const x = $derived<[number, number]>(xs.length ? [xs[0], xs[xs.length - 1] + stepMs] : [0, 1]);
	const y = $derived<[number, number]>([0, extent([...totals, baseline?.value])[1] * 1.08]);
	const anchors = $derived(xs.map((t) => t + stepMs / 2));
	const withTime = $derived(stepMs < DAY);
	const stacked = $derived(stacks.length > 1);

	function tip(i: number): Tip {
		const total = totals[i];
		return {
			title: formatInstant(xs[i], timezone, withTime),
			lead: total == null ? { value: 'No data' } : { value: format(total), status: status?.[i] ?? undefined, providers: providers?.[i] ?? undefined },
			rows: stacked ? stacks.map((s) => ({ label: s.label, value: s.ys[i] == null ? '–' : format(s.ys[i] ?? 0) })) : []
		};
	}

	function table(): TableData {
		return {
			columns: ['Window', ...stacks.map((s) => s.label), ...(stacked ? ['Total'] : [])],
			rows: xs
				.map((t, i) => [
					formatInstant(t, timezone, withTime),
					...stacks.map((s) => (s.ys[i] == null ? '–' : format(s.ys[i] ?? 0))),
					...(stacked ? [totals[i] == null ? '–' : format(totals[i] ?? 0)] : [])
				])
				.reverse()
		};
	}
</script>

<ChartFrame {label} xs={anchors} {x} {y} {timezone} {height} {tip} {table} {actions} {onselect} crosshair={false}>
	{#snippet legend()}
		{#if stacked}
			{#each stacks as s (s.label)}<span class="key"><span class={['swatch', s.color]}></span>{s.label}</span>{/each}
		{/if}
	{/snippet}
	{#snippet marks(f)}
		{@const w = Math.max(1, (f.sx(x[0] + stepMs) - f.sx(x[0])) * 0.72)}
		{@const r = Math.min(4, w / 3)}
		{#each xs as t, i (t)}
			{@const cx = f.sx(t + stepMs / 2) - w / 2}
			{#each stacks as s, k (k)}
				{@const below = stacks.slice(0, k).reduce((n, o) => n + (o.ys[i] ?? 0), 0)}
				{@const v = s.ys[i]}
				{#if v}
					<!-- Stacked segments keep a hairline gap; the top of a bar is rounded. -->
					<rect
						class={['bar', s.color ?? 'hue', f.active >= 0 && f.active !== i && 'dim']}
						x={cx}
						y={f.sy(below + v)}
						width={w}
						height={Math.max(0, f.sy(below) - f.sy(below + v) - (stacked ? 1 : 0))}
						rx={stacked ? Math.min(2, w / 4) : r}
					/>
				{/if}
			{/each}
			{#if i === picked && totals[i]}
				<rect class="picked" x={cx - 2} y={f.sy(totals[i] ?? 0) - 2} width={w + 4} height={f.sy(0) - f.sy(totals[i] ?? 0) + 4} rx={r + 2} />
			{/if}
		{/each}
		{#if baseline}
			<line class="baseline" x1={f.left} x2={f.right} y1={f.sy(baseline.value)} y2={f.sy(baseline.value)} />
			<text class="baseline-label" x={f.right - 4} y={f.sy(baseline.value) - 5} text-anchor="end">{baseline.label}</text>
		{/if}
	{/snippet}
</ChartFrame>

<style>
	.bar {
		transition: fill-opacity 120ms;
	}
	.bar.dim {
		fill-opacity: 0.55;
	}
	@media (prefers-reduced-motion: reduce) {
		.bar {
			transition: none;
		}
	}
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.hue {
		fill: var(--metric, var(--color-accent));
	}
	.deep {
		fill: var(--stage-deep);
		background: var(--stage-deep);
	}
	.light {
		fill: var(--stage-light);
		background: var(--stage-light);
	}
	.rem {
		fill: var(--stage-rem);
		background: var(--stage-rem);
	}
	.awake {
		fill: var(--stage-awake);
		background: var(--stage-awake);
	}
	.other {
		fill: var(--stage-other);
		background: var(--stage-other);
	}
	.accent {
		fill: var(--color-accent);
		background: var(--color-accent);
	}
	.info {
		fill: var(--color-info);
		background: var(--color-info);
	}
	.picked {
		fill: none;
		stroke: var(--color-text);
		stroke-width: 1.5;
	}
	.baseline {
		stroke: var(--color-text-muted);
		stroke-width: 1.5;
		stroke-dasharray: 4 4;
		stroke-opacity: 0.8;
	}
	.baseline-label {
		font-size: var(--text-2xs);
		fill: var(--color-text-muted);
	}
	.key {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
	}
</style>
