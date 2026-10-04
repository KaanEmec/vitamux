<!--
	Events on a shared time axis, one lane per source or event type (workouts, sessions,
	notifications). Each event is a bar with a tooltip; the events are also available as a table.
-->
<script lang="ts">
	import { sourceClass } from '../ui/source.ts';
	import ChartTable from './ChartTable.svelte';
	import { formatInstant, linear, measureRender } from './scale.ts';

	let {
		lanes,
		from,
		to,
		label,
		timezone
	}: {
		lanes: { label: string; source?: string; events: { start: number; end: number; label: string }[] }[];
		from: number;
		to: number;
		label: string;
		timezone?: string;
	} = $props();

	const width = 1000;
	const sx = $derived(linear([from, to], [0, width]));
	const table = () => ({
		columns: ['Lane', 'Event', 'Start', 'End'],
		rows: lanes.flatMap((l) => l.events.map((e) => [l.label, e.label, formatInstant(e.start, timezone), formatInstant(e.end, timezone)]))
	});

	const start = performance.now();
	$effect(() => measureRender(start));
</script>

<div class="lanes" role="img" aria-label={label}>
	{#each lanes as l, i (i)}
		<div class={['lane', l.source && sourceClass(l.source)]}>
			<span class="label">{l.label}</span>
			<svg viewBox="0 0 {width} 16" preserveAspectRatio="none" aria-hidden="true">
				<line class="track" x1="0" x2={width} y1="8" y2="8" />
				{#each l.events as e, j (j)}
					<rect x={sx(e.start)} width={Math.max(sx(e.end) - sx(e.start), 2)} y="2" height="12" rx="3">
						<title>{e.label}: {formatInstant(e.start, timezone)} – {formatInstant(e.end, timezone)}</title>
					</rect>
				{/each}
			</svg>
		</div>
	{/each}
</div>
<ChartTable caption={label} data={table} />

<style>
	.lanes {
		display: grid;
		gap: var(--space-2);
	}
	.lane {
		display: grid;
		grid-template-columns: 8rem minmax(0, 1fr);
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-xs);
	}
	.label {
		overflow: hidden;
		color: var(--color-text-muted);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	svg {
		display: block;
		width: 100%;
		height: 1rem;
	}
	.track {
		stroke: var(--chart-grid);
		vector-effect: non-scaling-stroke;
	}
	rect {
		fill: var(--src, var(--color-accent));
	}
</style>
