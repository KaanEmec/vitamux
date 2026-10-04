<!-- The data behind a chart as a table, on request (a <details>), capped at 1,000 rows. -->
<script lang="ts">
	import type { TableData } from './types.ts';

	let { caption, data }: { caption: string; data: () => TableData } = $props();

	let open = $state(false);
	const max = 1000;
	const table = $derived(open ? data() : null);
</script>

<details bind:open>
	<summary>Show as a table</summary>
	{#if table}
		<div class="scroll" tabindex="0" role="region" aria-label={caption}>
			<table>
				<caption class="visually-hidden">{caption}</caption>
				<thead>
					<tr>{#each table.columns as c, i (i)}<th scope="col">{c}</th>{/each}</tr>
				</thead>
				<tbody>
					{#each table.rows.slice(0, max) as r, i (i)}
						<tr>{#each r as cell, j (j)}<td>{cell}</td>{/each}</tr>
					{/each}
				</tbody>
			</table>
		</div>
		{#if table.rows.length > max}<p class="muted">First {max} of {table.rows.length} rows.</p>{/if}
	{/if}
</details>

<style>
	details {
		margin-top: var(--space-2);
		font-size: var(--text-sm);
	}
	summary {
		width: fit-content;
		color: var(--color-link);
		cursor: pointer;
	}
	.scroll {
		max-height: 20rem;
		margin-top: var(--space-2);
		overflow: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
	}
	th,
	td {
		padding: var(--space-1) var(--space-3);
		text-align: left;
		white-space: nowrap;
		border-bottom: 1px solid var(--color-border);
	}
	th {
		position: sticky;
		top: 0;
		font-weight: 500;
		color: var(--color-text-muted);
		background: var(--color-surface);
	}
	p {
		margin: var(--space-1) 0 0;
	}
</style>
