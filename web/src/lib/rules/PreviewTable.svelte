<!-- Per-day comparison of a draft rule with the active one (POST /resolution/preview). -->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import StatusIcon from '../components/StatusIcon.svelte';
	import type { PreviewDay } from './stubs.ts';

	let { days }: { days: PreviewDay[] } = $props();

	type Resolved = Schemas['ResolvedValue'];

	function show(r: Resolved): string {
		if (r.value === undefined || r.value === null) return 'no value';
		const v =
			typeof r.value === 'number'
				? String(Math.round(r.value * 100) / 100)
				: typeof r.value === 'object'
					? Object.entries(r.value as Record<string, unknown>)
							.map(([k, x]) => `${k} ${x}`)
							.join(', ')
					: String(r.value);
		return r.unit ? `${v} ${r.unit}` : v;
	}
	const source = (r: Resolved) => r.inputs?.find((i) => i.selected)?.group ?? '';
	const changed = (d: PreviewDay) =>
		JSON.stringify(d.draft.value) !== JSON.stringify(d.active.value) ||
		d.draft.status !== d.active.status ||
		source(d.draft) !== source(d.active);
	const count = $derived(days.filter(changed).length);
</script>

<p role="status"><strong>{count}</strong> of {days.length} days change with this draft.</p>
<table class="preview">
	<thead>
		<tr><th scope="col">Day</th><th scope="col">Active rule</th><th scope="col">Draft</th><th scope="col">Change</th></tr>
	</thead>
	<tbody>
		{#each days as d (d.local_date)}
			{@const diff = changed(d)}
			<tr class={{ changed: diff }}>
				<th scope="row">{d.local_date}</th>
				<td>
					{show(d.active)}{#if source(d.active)}<span class="muted">{` · ${source(d.active)}`}</span>{/if}
					<details><summary>Why</summary>{d.active.explanation}</details>
				</td>
				<td>
					{show(d.draft)}{#if source(d.draft)}<span class="muted">{` · ${source(d.draft)}`}</span>{/if}
					<details><summary>Why</summary>{d.draft.explanation}</details>
				</td>
				<td>
					{#if diff}<StatusIcon status="warn" /> Changed{:else}<StatusIcon status="ok" /> Same{/if}
				</td>
			</tr>
		{/each}
	</tbody>
</table>

<style>
	.preview {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2);
		border-bottom: 1px solid var(--color-border);
		text-align: left;
		vertical-align: top;
	}
	tr.changed {
		background: var(--color-surface-2);
	}
	details {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
</style>
