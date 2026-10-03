<!--
	Stage bars of one sleep session on a shared time axis, so several sources line up.
	Rows (awake on top) encode the stage; colour is secondary. Geometry is SVG attributes,
	because inline styles are blocked by the CSP.
-->
<script lang="ts" module>
	export const stageOrder = ['awake', 'rem', 'light', 'deep', 'asleep_unspecified', 'in_bed'] as const;
	export const stageLabels: Record<string, string> = {
		awake: 'Awake',
		rem: 'REM',
		light: 'Light',
		deep: 'Deep',
		asleep_unspecified: 'Asleep',
		in_bed: 'In bed'
	};
	export const rowHeight = 20;
</script>

<script lang="ts">
	import type { Schemas } from '../api/client.ts';

	let {
		stages,
		rows,
		from,
		to,
		label
	}: {
		stages: Schemas['SleepStage'][];
		/** Stage rows to draw, top to bottom (the same for every session compared). */
		rows: string[];
		/** Shared axis, epoch ms. */
		from: number;
		to: number;
		label: string;
	} = $props();

	const width = 1000;
	const span = $derived(Math.max(to - from, 1));
	const bars = $derived(
		stages.map((s) => {
			const a = Date.parse(s.start_at);
			const b = Date.parse(s.end_at);
			return {
				stage: s.stage,
				x: ((a - from) / span) * width,
				w: Math.max(((b - a) / span) * width, 0.5),
				y: rows.indexOf(s.stage) * rowHeight + 2,
				title: `${stageLabels[s.stage] ?? s.stage} ${Math.round((b - a) / 60000)} min`
			};
		})
	);
</script>

<svg
	class="hypnogram"
	viewBox="0 0 {width} {rows.length * rowHeight}"
	preserveAspectRatio="none"
	width="100%"
	height={rows.length * rowHeight}
	role="img"
	aria-label={label}
>
	{#each rows as r, i (r)}
		<line x1="0" x2={width} y1={(i + 1) * rowHeight} y2={(i + 1) * rowHeight} class="rule" />
	{/each}
	{#each bars as b, i (i)}
		<rect class={['bar', b.stage]} x={b.x} y={b.y} width={b.w} height={rowHeight - 4}><title>{b.title}</title></rect>
	{/each}
</svg>

<style>
	.hypnogram {
		display: block;
		background: var(--color-surface-2);
		border-radius: var(--radius-sm);
	}
	.rule {
		stroke: var(--color-border);
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
	}
	.bar {
		fill: var(--color-info);
	}
	.bar.awake {
		fill: var(--color-warn);
	}
	.bar.rem {
		fill: var(--color-accent);
	}
	.bar.light {
		fill: var(--color-neutral);
	}
	.bar.deep {
		fill: var(--color-info);
	}
	.bar.asleep_unspecified,
	.bar.in_bed {
		fill: var(--color-text-muted);
	}
</style>
