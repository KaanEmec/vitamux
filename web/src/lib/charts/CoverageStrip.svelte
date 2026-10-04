<!--
	Coverage per source and day: one row per source (its stable colour), one cell per day from
	`start`, shaded by the share of hours with data (0–1). Each row has a text summary and every
	cell a tooltip, so the value never depends on colour alone.
-->
<script lang="ts">
	import { sourceClass } from '../ui/source.ts';
	import { addDays } from '../data/format.ts';

	let { rows, start, caption }: { rows: { label: string; days: number[] }[]; start: string; caption: string } = $props();

	const cell = 6;
	const gap = 1;
	const days = $derived(Math.max(0, ...rows.map((r) => r.days.length)));
	const level = (c: number) => (!(c > 0) ? 0 : c < 0.25 ? 1 : c < 0.5 ? 2 : c < 0.75 ? 3 : 4);
	const daysWithData = (r: { days: number[] }) => r.days.filter((c) => c > 0).length;
</script>

<figure class="coverage">
	<figcaption class="visually-hidden">{caption}</figcaption>
	{#each rows as r (r.label)}
		<div class={['row', sourceClass(r.label)]}>
			<span class="label">{r.label}</span>
			<svg
				width={days * (cell + gap)}
				height={cell * 2 + 2}
				viewBox="0 0 {days * (cell + gap)} {cell * 2 + 2}"
				role="img"
				aria-label="{r.label}: data on {daysWithData(r)} of {r.days.length} days"
			>
				{#each r.days as c, i (i)}
					<rect class="l{level(c)}" x={i * (cell + gap)} y="0" width={cell} height={cell * 2 + 2} rx="1.5">
						<title>{addDays(start, i)}: {Math.round((c || 0) * 100)}%</title>
					</rect>
				{/each}
			</svg>
			<span class="summary muted">{daysWithData(r)}/{r.days.length} d</span>
		</div>
	{/each}
</figure>

<style>
	.coverage {
		display: grid;
		gap: var(--space-1);
		margin: 0;
	}
	.row {
		display: grid;
		grid-template-columns: 9rem auto auto;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-xs);
	}
	.label {
		overflow: hidden;
		font-family: var(--font-mono);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	svg {
		max-width: 100%;
	}
	rect {
		fill: var(--src);
	}
	.l0 {
		fill: var(--color-surface-2);
	}
	.l1 {
		fill-opacity: 0.25;
	}
	.l2 {
		fill-opacity: 0.5;
	}
	.l3 {
		fill-opacity: 0.75;
	}
</style>
