<!--
	A low–high pair per point, e.g. diastolic and systolic blood pressure: a filled dot (high), a
	ring (low) and a bar between them, in the metric hue. The tooltip leads with "high/low".
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { extent, formatInstant, formatNumber } from './scale.ts';
	import type { TableData, Tip, TipAction } from './types.ts';

	let {
		xs,
		lo,
		hi,
		label,
		loLabel = 'Low',
		hiLabel = 'High',
		unit = '',
		timezone,
		height,
		details,
		actions,
		onselect
	}: {
		xs: number[];
		lo: (number | null)[];
		hi: (number | null)[];
		label: string;
		loLabel?: string;
		hiLabel?: string;
		unit?: string;
		timezone?: string;
		height?: number;
		/** Extra tooltip rows and note of one point (pulse, posture, device). */
		details?: (i: number) => { rows?: Tip['rows']; note?: string };
		actions?: TipAction[];
		onselect?: (i: number) => void;
	} = $props();

	const span = $derived(xs.length > 1 ? xs[xs.length - 1] - xs[0] : 86_400_000);
	const x = $derived<[number, number]>(xs.length ? [xs[0] - span * 0.02, xs[xs.length - 1] + span * 0.02] : [0, 1]);
	const y = $derived(extent([...lo, ...hi], 0.1));
	const fmt = (v: number | null) => (v == null ? '–' : `${formatNumber(v)}${unit ? ` ${unit}` : ''}`);

	const tip = (i: number): Tip => ({
		title: formatInstant(xs[i], timezone),
		lead: hi[i] == null && lo[i] == null ? { value: 'No data' } : { value: `${hi[i] == null ? '–' : formatNumber(hi[i] ?? 0)}/${lo[i] == null ? '–' : formatNumber(lo[i] ?? 0)}`, unit },
		rows: [
			{ label: hiLabel, value: fmt(hi[i]) },
			{ label: loLabel, value: fmt(lo[i]) },
			...(details?.(i).rows ?? [])
		],
		note: details?.(i).note
	});
	const table = (): TableData => ({
		columns: ['Time', hiLabel, loLabel],
		rows: xs.map((t, i) => [formatInstant(t, timezone), fmt(hi[i]), fmt(lo[i])]).reverse()
	});
</script>

<ChartFrame {label} {xs} {x} {y} {timezone} {height} {tip} {table} {actions} {onselect}>
	{#snippet legend()}
		<span class="key"><span class="swatch hi"></span>{hiLabel}</span>
		<span class="key"><span class="swatch lo"></span>{loLabel}</span>
	{/snippet}
	{#snippet marks(f)}
		{#each xs as t, i (i)}
			{@const a = lo[i]}
			{@const b = hi[i]}
			{#if a != null && b != null}
				<line class={['stem', f.active === i && 'active']} x1={f.sx(t)} x2={f.sx(t)} y1={f.sy(a)} y2={f.sy(b)} />
				<circle class="hi" cx={f.sx(t)} cy={f.sy(b)} r="4" />
				<circle class="lo" cx={f.sx(t)} cy={f.sy(a)} r="4" />
			{/if}
		{/each}
	{/snippet}
</ChartFrame>

<style>
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.stem {
		stroke: var(--metric, var(--color-accent));
		stroke-opacity: 0.45;
		stroke-width: 3;
		stroke-linecap: round;
	}
	.stem.active {
		stroke-opacity: 1;
	}
	.hi {
		fill: var(--metric, var(--color-accent));
		background: var(--metric, var(--color-accent));
	}
	/* Low is a ring: position and shape both tell the two apart. */
	.lo {
		fill: var(--color-surface);
		stroke: var(--metric, var(--color-accent));
		stroke-width: 2;
		border: 2px solid var(--metric, var(--color-accent));
	}
	.key {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 50%;
	}
</style>
