<!--
	Bars per window (additive metrics: steps, energy), or stacked bars (sleep stages per night,
	`color` per stack). xs are window starts, evenly spaced. An optional baseline is drawn over them.
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { DAY, extent, formatInstant, formatNumber } from './scale.ts';
	import type { StageColor } from './sleep.ts';
	import type { TableData, Tip } from './types.ts';

	let {
		xs,
		stacks,
		label,
		unit = '',
		timezone,
		height,
		baseline,
		format = (v: number) => `${formatNumber(v)}${unit ? ` ${unit}` : ''}`,
		onselect
	}: {
		xs: number[];
		stacks: { label: string; ys: (number | null)[]; color?: StageColor | 'accent' | 'info' }[];
		label: string;
		unit?: string;
		timezone?: string;
		height?: number;
		baseline?: { value: number; label: string };
		format?: (v: number) => string;
		onselect?: (i: number) => void;
	} = $props();

	const stepMs = $derived(xs.length > 1 ? Math.min(...xs.slice(1).map((t, i) => t - xs[i])) : DAY);
	const totals = $derived(xs.map((_, i) => stacks.reduce((n, s) => n + (s.ys[i] ?? 0), 0)));
	const x = $derived<[number, number]>(xs.length ? [xs[0], xs[xs.length - 1] + stepMs] : [0, 1]);
	const y = $derived<[number, number]>([0, extent([...totals, baseline?.value])[1] * 1.08]);
	const anchors = $derived(xs.map((t) => t + stepMs / 2));
	const withTime = $derived(stepMs < DAY);

	function tip(i: number): Tip {
		const rows = stacks.map((s) => ({ label: s.label, value: s.ys[i] == null ? '–' : format(s.ys[i] ?? 0) }));
		if (stacks.length > 1) rows.push({ label: 'Total', value: format(totals[i]) });
		return { title: formatInstant(xs[i], timezone, withTime), rows };
	}

	function table(): TableData {
		return {
			columns: ['Window', ...stacks.map((s) => s.label), ...(stacks.length > 1 ? ['Total'] : [])],
			rows: xs
				.map((t, i) => [
					formatInstant(t, timezone, withTime),
					...stacks.map((s) => (s.ys[i] == null ? '–' : format(s.ys[i] ?? 0))),
					...(stacks.length > 1 ? [format(totals[i])] : [])
				])
				.reverse()
		};
	}
</script>

<ChartFrame {label} xs={anchors} {x} {y} {timezone} {height} {tip} {table} {onselect}>
	{#snippet legend()}
		{#if stacks.length > 1}
			{#each stacks as s (s.label)}<span class="key"><span class={['swatch', s.color]}></span>{s.label}</span>{/each}
		{/if}
	{/snippet}
	{#snippet marks(f)}
		{@const w = Math.max(1, (f.sx(x[0] + stepMs) - f.sx(x[0])) * 0.72)}
		{#each xs as t, i (t)}
			{@const cx = f.sx(t + stepMs / 2) - w / 2}
			{#each stacks as s, k (k)}
				{@const below = stacks.slice(0, k).reduce((n, o) => n + (o.ys[i] ?? 0), 0)}
				{@const v = s.ys[i]}
				{#if v}
					<rect
						class={['bar', s.color, f.active === i && 'active']}
						x={cx}
						y={f.sy(below + v)}
						width={w}
						height={Math.max(0, f.sy(below) - f.sy(below + v))}
						rx={stacks.length > 1 ? 0 : Math.min(3, w / 3)}
					/>
				{/if}
			{/each}
		{/each}
		{#if baseline}<line class="baseline" x1={f.left} x2={f.right} y1={f.sy(baseline.value)} y2={f.sy(baseline.value)} />{/if}
	{/snippet}
</ChartFrame>

<style>
	.bar {
		fill: var(--color-accent);
		fill-opacity: 0.8;
	}
	.bar.active {
		fill-opacity: 1;
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
	.baseline {
		stroke: var(--color-text);
		stroke-dasharray: 3 3;
		stroke-opacity: 0.5;
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
