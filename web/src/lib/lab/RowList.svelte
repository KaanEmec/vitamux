<!--
	The extracted rows of a run, one line each: label, value and unit as printed, the extractor's
	confidence, how many checks to compare with the PDF, and the review state. The selected row
	is outlined; J and K (the page's keyboard flow) move it, so it scrolls into view.
-->
<script lang="ts">
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import type { Row } from './api.ts';
	import { printedValue, rowStatus } from './format.ts';

	let { rows, selected, onselect }: { rows: Row[]; selected: number | null; onselect: (row: Row) => void } = $props();

	let list = $state<HTMLDivElement>();
	$effect(() => {
		if (selected !== null) list?.querySelector('[aria-current="true"]')?.scrollIntoView({ block: 'nearest' });
	});
</script>

<div class="list" bind:this={list}>
	<table>
		<caption class="visually-hidden">Extracted rows</caption>
		<thead>
			<tr>
				<th scope="col">Row</th><th scope="col">Label as printed</th><th scope="col">Value as printed</th>
				<th scope="col" class="conf"><abbr title="Extractor hint only; every row is reviewed">Confidence</abbr></th>
				<th scope="col">Checks</th><th scope="col">Review</th>
			</tr>
		</thead>
		<tbody>
			{#each rows as r (r.index)}
				{@const rs = rowStatus[r.review_status]}
				{@const checks = r.validation.length + r.warnings.length}
				<tr class={[r.index === selected && 'current', r.review_status === 'rejected' && 'rejected']} aria-current={r.index === selected ? 'true' : undefined}>
					<td><button class="btn link" type="button" onclick={() => onselect(r)} aria-label="Review row {r.index + 1}: {r.analyte_label}">{r.index + 1}</button></td>
					<th scope="row">{r.analyte_label}</th>
					<td>{printedValue(r)} <span class="muted">{r.unit_text ?? ''}</span></td>
					<td class="conf">{r.confidence.toFixed(2)}</td>
					<td>{#if checks}<span class="status"><StatusIcon status="warn" /> {checks}</span>{:else}–{/if}</td>
					<td><span class="status"><StatusIcon status={rs.status} /> {rs.label}</span></td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<style>
	.list {
		max-height: 20rem;
		overflow: auto;
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-lg);
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
	}
	thead th {
		position: sticky;
		top: 0;
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--color-text-muted);
		background: var(--color-surface);
	}
	tbody tr:last-child > * {
		border-bottom: 0;
	}
	abbr {
		text-decoration: none;
	}
	tr.current > * {
		background: var(--color-accent-soft);
	}
	tr.current > :first-child {
		box-shadow: inset 3px 0 var(--color-accent);
	}
	tr.rejected th,
	tr.rejected td:nth-child(3) {
		text-decoration: line-through;
		color: var(--color-text-muted);
	}
	/* The editor shows the confidence too; the narrow list keeps the review state in view. */
	@media (max-width: 40rem) {
		.conf {
			display: none;
		}
	}
	.status {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		white-space: nowrap;
	}
</style>
