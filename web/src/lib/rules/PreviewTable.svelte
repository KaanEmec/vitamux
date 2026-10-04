<!-- Per-day comparison of a draft rule with the active one (POST /resolution/preview). -->
<script lang="ts">
	import StatusIcon from '../components/StatusIcon.svelte';
	import { dayChanged, selectedGroup, showResolved } from './preview.ts';
	import type { PreviewDay } from './stubs.ts';

	let { days }: { days: PreviewDay[] } = $props();

	const count = $derived(days.filter(dayChanged).length);
</script>

<p role="status"><strong>{count}</strong> of {days.length} days change with this draft.</p>
<div class="scroll">
	<table class="preview">
		<thead>
			<tr><th scope="col">Day</th><th scope="col">Active rule</th><th scope="col">Draft</th><th scope="col">Change</th></tr>
		</thead>
		<tbody>
			{#each days as d (d.local_date)}
				{@const diff = dayChanged(d)}
				<tr class={{ changed: diff }}>
					<th scope="row">{d.local_date}</th>
					<td>
						{showResolved(d.active)}{#if selectedGroup(d.active)}<span class="muted">{` · ${selectedGroup(d.active)}`}</span>{/if}
						<details><summary>Why</summary>{d.active.explanation}</details>
					</td>
					<td>
						{showResolved(d.draft)}{#if selectedGroup(d.draft)}<span class="muted">{` · ${selectedGroup(d.draft)}`}</span>{/if}
						<details><summary>Why</summary>{d.draft.explanation}</details>
					</td>
					<td>
						{#if diff}<StatusIcon status="warn" /> Changed{:else}<StatusIcon status="ok" /> Same{/if}
					</td>
				</tr>
			{/each}
		</tbody>
	</table>
</div>

<style>
	.scroll {
		overflow-x: auto;
	}
	.preview {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		border-bottom: 1px solid var(--color-border);
		text-align: left;
		vertical-align: top;
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	tr.changed {
		background: var(--color-draft-bg);
	}
	details {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
</style>
