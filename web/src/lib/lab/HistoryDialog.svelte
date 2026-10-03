<!-- Revisions of one confirmed lab result, newest (current) first, with their provenance. -->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Problem } from '#lib/api/client.ts';
	import Modal from '#lib/components/Modal.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { resultHistory, type LabResult } from './api.ts';
	import { printedValue, providerName, when } from './format.ts';

	let { result, onclose }: { result: LabResult; onclose: () => void } = $props();

	let revisions = $state<LabResult[] | null>(null);
	let problem = $state<Problem | null>(null);

	onMount(async () => {
		const res = await resultHistory(result.id);
		problem = res.problem;
		revisions = res.data;
	});
</script>

<Modal title="History of {result.original_label}" {onclose}>
	<ProblemAlert {problem} />
	{#if revisions === null && !problem}
		<p class="muted" role="status">Loading history…</p>
	{:else if revisions}
		<table>
			<caption class="visually-hidden">Revisions</caption>
			<thead>
				<tr>
					<th scope="col">Revision</th><th scope="col">Label</th><th scope="col">Value</th><th scope="col">Unit</th>
					<th scope="col">Range as printed</th><th scope="col">Flag as printed</th><th scope="col">Written</th>
				</tr>
			</thead>
			<tbody>
				{#each revisions as r, i (r.revision)}
					<tr>
						<th scope="row">{r.revision}{i === 0 ? ' (current)' : ''}</th>
						<td>{r.original_label}</td>
						<td>{printedValue(r)}</td>
						<td>{r.unit_text ?? 'unitless'}</td>
						<td>{r.reference_range_text ?? '–'}</td>
						<td>{r.printed_flag ?? '–'}</td>
						<td>{when(r.updated_at)}</td>
					</tr>
				{/each}
			</tbody>
		</table>
		<p class="muted provenance">
			Confirmed by {result.provenance.confirmed_by} on {when(result.provenance.confirmed_at)} from page {result.page ?? '–'}, read by
			{providerName(result.provenance.provider)}{result.provenance.model ? ` (${result.provenance.model})` : ''}, prompt
			{result.provenance.prompt_version}, schema {result.provenance.schema_version}.
		</p>
		{#if result.evidence_text}<p class="muted">Printed text: <q>{result.evidence_text}</q></p>{/if}
	{/if}
</Modal>

<style>
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-1) var(--space-2);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
	}
	.provenance {
		margin-top: var(--space-3);
	}
</style>
