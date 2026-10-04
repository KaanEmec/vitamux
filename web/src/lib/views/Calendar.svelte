<!--
	A month of local dates, Monday first. Days with `counts` are buttons that pick (or clear) that
	date; the month moves with the arrows. Dates are YYYY-MM-DD, months YYYY-MM.
-->
<script lang="ts" module>
	import { addDays } from '../data/format.ts';

	/** The month `n` months from `month`. */
	export function addMonths(month: string, n: number): string {
		const [y, m] = month.split('-').map(Number);
		const t = new Date(Date.UTC(y, m - 1 + n, 1));
		return `${t.getUTCFullYear()}-${String(t.getUTCMonth() + 1).padStart(2, '0')}`;
	}

	/** First and last date of a month. */
	export const monthRange = (month: string) => ({ start: `${month}-01`, end: addDays(`${addMonths(month, 1)}-01`, -1) });
</script>

<script lang="ts">
	let {
		month,
		counts,
		picked,
		unit,
		onmonth,
		onpick
	}: {
		month: string;
		counts: Record<string, number>;
		picked: string | null;
		/** What the counts count, for screen readers ("workout"). */
		unit: string;
		onmonth: (month: string) => void;
		onpick: (date: string | null) => void;
	} = $props();

	const title = $derived(new Intl.DateTimeFormat(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' }).format(Date.parse(`${month}-01T00:00:00Z`)));
	const weekdays = $derived(
		Array.from({ length: 7 }, (_, i) => new Intl.DateTimeFormat(undefined, { weekday: 'short', timeZone: 'UTC' }).format(Date.UTC(2024, 0, 1 + i)))
	);
	const weeks = $derived.by(() => {
		const { start, end } = monthRange(month);
		const lead = (new Date(`${start}T00:00:00Z`).getUTCDay() + 6) % 7;
		const cells: (string | null)[] = Array.from({ length: lead }, () => null);
		for (let d = start; d <= end; d = addDays(d, 1)) cells.push(d);
		while (cells.length % 7) cells.push(null);
		return Array.from({ length: cells.length / 7 }, (_, i) => cells.slice(i * 7, i * 7 + 7));
	});
</script>

<div class="calendar">
	<div class="nav">
		<button class="btn sm" type="button" onclick={() => onmonth(addMonths(month, -1))}>Previous<span class="visually-hidden"> month</span></button>
		<h3 aria-live="polite">{title}</h3>
		<button class="btn sm" type="button" onclick={() => onmonth(addMonths(month, 1))}>Next<span class="visually-hidden"> month</span></button>
	</div>
	<table>
		<thead>
			<tr>{#each weekdays as d (d)}<th scope="col">{d}</th>{/each}</tr>
		</thead>
		<tbody>
			{#each weeks as week, i (i)}
				<tr>
					{#each week as date, j (j)}
						<td>
							{#if date}
								{@const n = counts[date] ?? 0}
								{#if n}
									<button
										type="button"
										class={['day', date === picked && 'picked']}
										aria-pressed={date === picked}
										aria-label="{date}, {n} {unit}{n === 1 ? '' : 's'}"
										onclick={() => onpick(date === picked ? null : date)}
									>
										{Number(date.slice(8))}<span class="count" aria-hidden="true">{n}</span>
									</button>
								{:else}
									<span class="day empty">{Number(date.slice(8))}</span>
								{/if}
							{/if}
						</td>
					{/each}
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<style>
	.nav {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		margin-bottom: var(--space-3);
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
	table {
		width: 100%;
		table-layout: fixed;
		border-collapse: separate;
		border-spacing: var(--space-1);
	}
	th {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	td {
		padding: 0;
	}
	.day {
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		width: 100%;
		min-height: 2.75rem;
		padding: var(--space-1);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text);
		background: var(--color-inset);
		border: 1px solid transparent;
		border-radius: var(--radius-sm);
	}
	button.day {
		cursor: pointer;
		border-color: var(--color-border-strong);
	}
	button.day:hover {
		background: var(--color-surface-2);
	}
	button.day.picked {
		color: var(--color-on-accent);
		background: var(--color-accent);
		border-color: var(--color-accent);
	}
	.empty {
		color: var(--color-text-faint);
		background: transparent;
	}
	.count {
		font-size: var(--text-2xs);
		font-weight: 600;
		color: var(--color-link);
	}
	.picked .count {
		color: inherit;
	}
</style>
