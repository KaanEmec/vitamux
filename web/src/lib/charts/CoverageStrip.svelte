<!--
	Coverage per source and day: one row per source (its stable colour), one cell per day from
	`start` (or per `noun`, e.g. the Day view's buckets), shaded by the share of hours with data (0–1). A row with `sources` is a source-per-window
	strip instead, as wide as its container: each cell takes the colour of the source behind that
	day's value (null: none), with a key under it. Each row has a text summary and every cell a
	tooltip, so the value never depends on colour alone.
-->
<script lang="ts">
	import { sourceClass } from '../ui/source.ts';
	import { providerLabel } from '../connections/connections.ts';
	import { addDays } from '../data/format.ts';

	type Row = { label: string; days: number[]; sources?: (string | null)[] };
	let {
		rows,
		start,
		caption,
		noun = 'days',
		cellLabel = (i: number) => addDays(start, i)
	}: {
		rows: Row[];
		start: string;
		caption: string;
		/** What a cell is ("buckets" on the Day view). */
		noun?: string;
		cellLabel?: (i: number) => string;
	} = $props();

	const cell = 6;
	const gap = 1;
	const days = $derived(Math.max(0, ...rows.map((r) => r.days.length)));
	const level = (c: number) => (!(c > 0) ? 0 : c < 0.25 ? 1 : c < 0.5 ? 2 : c < 0.75 ? 3 : 4);
	const daysWithData = (r: Row) => r.days.filter((c) => c > 0).length;
	/** Days per source of a source-per-window row, most first. */
	const tally = (r: Row) => {
		const n: Record<string, number> = {};
		for (const s of r.sources ?? []) if (s) n[s] = (n[s] ?? 0) + 1;
		return Object.entries(n).sort((a, b) => b[1] - a[1]);
	};
	const summary = (r: Row) =>
		r.sources
			? `${r.label}: ${[...tally(r).map(([s, n]) => `${providerLabel(s)} ${n}`), `none ${r.sources.filter((s) => !s).length}`].join(', ')} of ${r.days.length} ${noun}`
			: `${r.label}: data on ${daysWithData(r)} of ${r.days.length} ${noun}`;
	const cellTitle = (r: Row, i: number, c: number) =>
		`${cellLabel(i)}: ${r.sources ? (r.sources[i] ? providerLabel(r.sources[i]) : 'no value') : `${Math.round((c || 0) * 100)}%`}`;
</script>

<figure class="coverage">
	<figcaption class="visually-hidden">{caption}</figcaption>
	{#each rows as r (r.label)}
		<div class={['row', r.sources ? 'picks' : sourceClass(r.label)]}>
			<span class="label">{r.label}</span>
			<svg
				width={r.sources ? '100%' : days * (cell + gap)}
				height={cell * 2 + 2}
				viewBox="0 0 {days * (cell + gap)} {cell * 2 + 2}"
				preserveAspectRatio={r.sources ? 'none' : undefined}
				role="img"
				aria-label={summary(r)}
			>
				{#each r.days as c, i (i)}
					{@const s = r.sources?.[i]}
					<rect class={[`l${level(c)}`, s && sourceClass(s)]} x={i * (cell + gap)} y="0" width={cell} height={cell * 2 + 2} rx="1.5">
						<title>{cellTitle(r, i, c)}</title>
					</rect>
				{/each}
			</svg>
			<span class="summary muted">{daysWithData(r)}/{r.days.length}{noun === 'days' ? ' d' : ''}</span>
			{#if r.sources}
				<span class="key" aria-hidden="true">
					{#each tally(r) as [s] (s)}<span class={sourceClass(s)}><i></i>{providerLabel(s)}</span>{/each}
					<span class="none"><i></i>None</span>
				</span>
			{/if}
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
	/* A source-per-window strip spans the width it has. */
	.picks {
		grid-template-columns: 9rem minmax(0, 1fr) auto;
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
	.key {
		display: flex;
		flex-wrap: wrap;
		grid-column: 1 / -1;
		gap: var(--space-1) var(--space-3);
		color: var(--color-text-muted);
	}
	.key span {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
	}
	.key i {
		width: 0.5rem;
		height: 0.5rem;
		background: var(--src);
		border-radius: 2px;
	}
	.key .none i {
		background: var(--color-surface-2);
	}
</style>
