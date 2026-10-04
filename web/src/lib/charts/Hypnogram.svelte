<!--
	Stage bars of one sleep session on a shared time axis, so several sources line up. Rows
	(awake on top) encode the stage, labelled on the left; colour is secondary. The stages are
	also available as a table.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import ChartTable from './ChartTable.svelte';
	import { formatInstant, measureRender } from './scale.ts';
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

	const width = 1000;
	const rowHeight = 20;
	const span = $derived(Math.max(to - from, 1));
	const bars = $derived(
		stages.map((s) => {
			const a = Date.parse(s.start_at);
			const b = Date.parse(s.end_at);
			return {
				color: stageColor(s.stage),
				x: ((a - from) / span) * width,
				w: Math.max(((b - a) / span) * width, 0.5),
				y: rows.indexOf(s.stage) * rowHeight + 3,
				title: `${stageLabels[s.stage] ?? s.stage} ${Math.round((b - a) / 60000)} min`
			};
		})
	);
	const table = () => ({
		columns: ['Start', 'End', 'Stage'],
		rows: stages.map((s) => [formatInstant(Date.parse(s.start_at), timezone), formatInstant(Date.parse(s.end_at), timezone), stageLabels[s.stage] ?? s.stage])
	});

	const start = performance.now();
	$effect(() => measureRender(start));
</script>

<div class="hypnogram">
	<ul class="labels" aria-hidden="true">
		{#each rows as r (r)}<li>{stageLabels[r] ?? r}</li>{/each}
	</ul>
	<svg viewBox="0 0 {width} {rows.length * rowHeight}" preserveAspectRatio="none" height={rows.length * rowHeight} role="img" aria-label={label}>
		{#each rows as r, i (r)}
			<line x1="0" x2={width} y1={(i + 0.5) * rowHeight} y2={(i + 0.5) * rowHeight} class="rule" />
		{/each}
		{#each bars as b, i (i)}
			<rect class={['bar', b.color]} x={b.x} y={b.y} width={b.w} height={rowHeight - 6} rx="2"><title>{b.title}</title></rect>
		{/each}
	</svg>
</div>
<ChartTable caption={label} data={table} />

<style>
	.hypnogram {
		display: grid;
		grid-template-columns: 4rem minmax(0, 1fr);
		gap: var(--space-2);
	}
	.labels {
		display: grid;
		grid-auto-rows: 20px; /* rowHeight */
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: var(--text-2xs);
		line-height: 20px;
		color: var(--color-text-muted);
	}
	svg {
		display: block;
		width: 100%;
	}
	.rule {
		stroke: var(--chart-grid);
		vector-effect: non-scaling-stroke;
	}
	.bar.deep {
		fill: var(--stage-deep);
	}
	.bar.light {
		fill: var(--stage-light);
	}
	.bar.rem {
		fill: var(--stage-rem);
	}
	.bar.awake {
		fill: var(--stage-awake);
	}
	.bar.other {
		fill: var(--stage-other);
	}
</style>
