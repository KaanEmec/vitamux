<!--
	Bed and wake times per night: one bar from bed time down to wake time on a clock axis (evening
	at the top), with the mean of each as a dashed line. The picked night is outlined.
-->
<script lang="ts">
	import ChartFrame from '../charts/ChartFrame.svelte';
	import { DAY, extent, formatInstant } from '../charts/scale.ts';
	import type { TableData, Tip } from '../charts/types.ts';
	import { dayMs, mean } from './format.ts';
	import { clockText, type Span } from './sleep.ts';

	let {
		nights,
		picked,
		label,
		onselect
	}: {
		/** Ascending local dates; `span` is null for a night without an episode. */
		nights: { date: string; span: Span | null }[];
		picked?: string;
		label: string;
		onselect?: (i: number) => void;
	} = $props();

	// The axis runs downwards in time: values are negated so that earlier clock times sit higher.
	const xs = $derived(nights.map((n) => dayMs(n.date)));
	const spans = $derived(nights.flatMap((n) => n.span ?? []));
	const meanBed = $derived(mean(spans.map((s) => s.bed)));
	const meanWake = $derived(mean(spans.map((s) => s.wake)));
	const x = $derived<[number, number]>(xs.length ? [xs[0] - DAY / 2, xs[xs.length - 1] + DAY / 2] : [0, 1]);
	const y = $derived(extent(spans.flatMap((s) => [-s.bed, -s.wake]), 0.08));
	// A tick every two clock hours (the axis is negated, so hour h sits at -h).
	const hours = $derived.by(() => {
		const out: number[] = [];
		for (let h = Math.ceil(-y[1]); h <= -y[0]; h++) if (h % 2 === 0) out.push(-h);
		return out;
	});
	const night = (i: number) => formatInstant(xs[i], 'UTC', false);

	const tip = (i: number): Tip => {
		const s = nights[i].span;
		return {
			title: night(i),
			rows: s
				? [
						{ label: 'Bed', value: clockText(s.bed) },
						{ label: 'Wake', value: clockText(s.wake) }
					]
				: [{ label: 'Episode', value: 'none' }]
		};
	};
	const table = (): TableData => ({
		columns: ['Night', 'Bed', 'Wake'],
		rows: nights.map((n, i) => [night(i), n.span ? clockText(n.span.bed) : '–', n.span ? clockText(n.span.wake) : '–']).reverse()
	});
</script>

<ChartFrame {label} {xs} {x} {y} timezone="UTC" height={300} {tip} {table} {onselect} yFormat={(v) => clockText(-v)} yTicks={hours}>
	{#snippet legend()}
		<span class="key"><span class="swatch"></span>Bed to wake</span>
		<span class="key"><svg width="16" height="8" aria-hidden="true"><line x1="0" x2="16" y1="4" y2="4" /></svg>Mean bed and wake time</span>
	{/snippet}
	{#snippet marks(f)}
		{@const w = Math.max(2, (f.sx(x[0] + DAY) - f.sx(x[0])) * 0.6)}
		{#each nights as n, i (n.date)}
			{#if n.span}
				<rect
					class={['bar', f.active === i && 'active', n.date === picked && 'picked']}
					x={f.sx(xs[i]) - w / 2}
					y={f.sy(-n.span.bed)}
					width={w}
					height={Math.max(2, f.sy(-n.span.wake) - f.sy(-n.span.bed))}
					rx={Math.min(3, w / 2)}
				/>
			{/if}
		{/each}
		{#each [meanBed, meanWake] as m, k (k)}
			{#if m != null}<line class="mean" x1={f.left} x2={f.right} y1={f.sy(-m)} y2={f.sy(-m)} />{/if}
		{/each}
	{/snippet}
</ChartFrame>

<style>
	.bar {
		fill: var(--stage-light);
		fill-opacity: 0.7;
	}
	.bar.active {
		fill-opacity: 1;
	}
	.bar.picked {
		stroke: var(--color-text);
		stroke-width: 1.5;
	}
	.mean {
		stroke: var(--color-text-muted);
		stroke-dasharray: 3 3;
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
		background: var(--stage-light);
		border-radius: 2px;
	}
</style>
