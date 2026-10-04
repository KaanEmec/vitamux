<!--
	Stage bars of one sleep session on a shared time axis, so several sources line up. Rows
	(awake on top) encode the stage, labelled on the left; colour is secondary. The arrow keys and
	the pointer move from stage to stage; the stages are also available as a table.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import ChartFrame from './ChartFrame.svelte';
	import { formatClock, formatInstant, nearest } from './scale.ts';
	import { stageColor, stageLabels } from './sleep.ts';

	let {
		stages,
		rows,
		from,
		to,
		label,
		timezone
	}: {
		stages: Schemas['SleepStage'][];
		/** Stage rows to draw, top to bottom (the same for every session compared). */
		rows: string[];
		/** Shared axis, epoch ms. */
		from: number;
		to: number;
		label: string;
		timezone?: string;
	} = $props();

	const bars = $derived(
		stages
			.map((s) => ({ stage: s.stage, a: Date.parse(s.start_at), b: Date.parse(s.end_at), row: rows.indexOf(s.stage) }))
			.sort((p, q) => p.a - q.a)
	);
	const n = $derived(rows.length);
	const mids = $derived(bars.map((s) => (s.a + s.b) / 2));
	const name = (stage: string) => stageLabels[stage] ?? stage;
	const minutes = (s: { a: number; b: number }) => Math.round((s.b - s.a) / 60_000);
	const pick = (t: number) => {
		const i = bars.findIndex((s) => s.a <= t && t < s.b);
		return i >= 0 ? i : nearest(mids, t);
	};
	const tip = (i: number) => ({
		title: `${formatClock(bars[i].a, timezone)} – ${formatClock(bars[i].b, timezone)}`,
		lead: { value: name(bars[i].stage), unit: `${minutes(bars[i])} min` },
		rows: []
	});
	const table = () => ({
		columns: ['Start', 'End', 'Stage'],
		rows: bars.map((s) => [formatInstant(s.a, timezone), formatInstant(s.b, timezone), name(s.stage)])
	});
</script>

<ChartFrame
	{label}
	xs={mids}
	x={[from, to]}
	y={[0, n]}
	{timezone}
	height={n * 22 + 38}
	padding={{ left: 56 }}
	crosshair={false}
	{pick}
	{tip}
	{table}
	yTicks={rows.map((_, i) => n - i - 0.5)}
	yFormat={(v) => name(rows[Math.round(n - 0.5 - v)] ?? '')}
>
	{#snippet marks(f)}
		{@const h = f.sy(0) - f.sy(1)}
		{#each bars as s, i (i)}
			{#if s.row >= 0}
				<rect
					class={['bar', stageColor(s.stage), f.active === i && 'active']}
					x={f.sx(s.a)}
					y={f.sy(n - s.row) + 3}
					width={Math.max(f.sx(s.b) - f.sx(s.a), 0.75)}
					height={Math.max(h - 6, 1)}
					rx="2"
				/>
			{/if}
		{/each}
	{/snippet}
</ChartFrame>

<style>
	.bar.active {
		stroke: var(--color-text);
		stroke-width: 1.5;
	}
	.deep {
		fill: var(--stage-deep);
	}
	.light {
		fill: var(--stage-light);
	}
	.rem {
		fill: var(--stage-rem);
	}
	.awake {
		fill: var(--stage-awake);
	}
	.other {
		fill: var(--stage-other);
	}
</style>
