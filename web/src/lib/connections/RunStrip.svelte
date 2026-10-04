<!--
	The last 14 days of sync runs, one cell per local day, oldest first: filled when the day had
	successful runs and none that failed, hatched when one failed (a shape, not only a colour),
	empty without runs. `runs` is undefined while loading and null when the API did not answer.
	The text summary is the accessible name; each cell has the day's counts as a tooltip.
-->
<script lang="ts">
	import { DAYS, runDays, type Run } from './runs.ts';

	let { runs, large = false }: { runs: Run[] | null | undefined; large?: boolean } = $props();

	const days = $derived(runDays(runs ?? []));
	const ok = $derived(days.filter((d) => d.succeeded && !d.failed).length);
	const failed = $derived(days.filter((d) => d.failed).length);
	const idle = $derived(DAYS - ok - failed);
	const total = $derived(days.reduce((n, d) => n + d.succeeded + d.failed, 0));
	const plural = (n: number, what: string) => `${n} ${what}${n === 1 ? '' : 's'}`;
	const label = $derived(
		runs === undefined
			? 'Sync runs: loading'
			: runs === null
				? 'Sync runs are not available'
				: `Sync runs, last ${DAYS} days: ${plural(ok, 'day')} with successful runs, ${plural(failed, 'day')} with a failed run, ${plural(idle, 'day')} without runs`
	);
	const tip = (d: { date: string; succeeded: number; failed: number }) =>
		`${d.date}: ${d.succeeded || d.failed ? `${d.succeeded} succeeded, ${d.failed} failed` : 'no runs'}`;
</script>

<div class="strip">
	<div class="head muted"><span>Sync runs · {DAYS} days</span>{#if runs}<span>{plural(total, 'run')}</span>{/if}</div>
	<div class={['cells', large && 'large']} role="img" aria-label={label} aria-busy={runs === undefined || undefined}>
		{#each days as d (d.date)}
			<span class={['cell', d.failed ? 'fail' : d.succeeded ? 'ok' : 'none']} title={tip(d)}></span>
		{/each}
	</div>
	{#if large}<div class="axis muted" aria-hidden="true"><span>{days[0].date}</span><span>Today</span></div>{/if}
</div>

<style>
	.head,
	.axis {
		display: flex;
		justify-content: space-between;
		font-size: var(--text-xs);
	}
	.head {
		margin-bottom: var(--space-2);
	}
	.cells {
		display: grid;
		grid-template-columns: repeat(14, minmax(0, 1fr));
		gap: 3px;
	}
	.cell {
		height: 1.125rem;
		border-radius: 3px;
	}
	.large .cell {
		height: 2rem;
		border-radius: var(--radius-xs);
	}
	.axis {
		margin-top: var(--space-1);
	}
	.none {
		background: var(--color-surface-2);
	}
	.ok {
		background: color-mix(in srgb, var(--color-accent) 60%, var(--color-surface-2));
	}
	.fail {
		background:
			repeating-linear-gradient(135deg, var(--color-warn) 0 2px, transparent 2px 5px),
			color-mix(in srgb, var(--color-warn) 30%, var(--color-surface-2));
	}
</style>
