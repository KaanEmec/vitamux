<!--
	Source × day coverage heatmap (one row per source, one cell per day from `start`).
	Coverage is 0–1; cells use five shades plus a per-cell tooltip, and a text summary per
	row so the value never depends on colour alone. SVG attributes, no inline styles (CSP).
-->
<script lang="ts">
	let { rows, start, caption }: { rows: { label: string; days: number[] }[]; start: string; caption: string } = $props();

	const cell = 6;
	const gap = 1;
	const days = $derived(Math.max(0, ...rows.map((r) => r.days.length)));

	function level(c: number): number {
		if (!(c > 0)) return 0;
		return c < 0.25 ? 1 : c < 0.5 ? 2 : c < 0.75 ? 3 : 4;
	}
	function dateAt(i: number): string {
		const [y, m, d] = start.split('-').map(Number);
		const t = new Date(Date.UTC(y, m - 1, d + i));
		return t.toISOString().slice(0, 10);
	}
	const daysWithData = (r: { days: number[] }) => r.days.filter((c) => c > 0).length;
</script>

<figure class="heatmap">
	<figcaption class="visually-hidden">{caption}</figcaption>
	{#each rows as r (r.label)}
		<div class="row">
			<span class="label">{r.label}</span>
			<svg
				width={days * (cell + gap)}
				height={cell * 2}
				viewBox="0 0 {days * (cell + gap)} {cell * 2}"
				role="img"
				aria-label="{r.label}: data on {daysWithData(r)} of {r.days.length} days"
			>
				{#each r.days as c, i (i)}
					<rect class="l{level(c)}" x={i * (cell + gap)} y="0" width={cell} height={cell * 2} rx="1">
						<title>{dateAt(i)}: {Math.round((c || 0) * 100)}%</title>
					</rect>
				{/each}
			</svg>
			<span class="summary muted">{daysWithData(r)}/{r.days.length} d</span>
		</div>
	{/each}
</figure>

<style>
	.heatmap {
		margin: 0;
		display: grid;
		gap: var(--space-1);
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
		text-overflow: ellipsis;
		white-space: nowrap;
		font-family: var(--font-mono);
	}
	svg {
		max-width: 100%;
	}
	rect {
		fill: var(--color-accent);
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
