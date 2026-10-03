<!-- Field-level diff of two rule specs (diffSpecs), as a table. -->
<script lang="ts">
	import { diffSpecs } from './rule.ts';

	let { before, after, caption }: { before: unknown; after: unknown; caption: string } = $props();

	const changes = $derived(diffSpecs(before, after));
	const show = (v: unknown) => (v === undefined ? '' : JSON.stringify(v));
</script>

{#if changes.length === 0}
	<p class="muted">{caption}: no differences.</p>
{:else}
	<table class="diff">
		<caption>{caption}</caption>
		<thead><tr><th scope="col">Field</th><th scope="col">Before</th><th scope="col">After</th></tr></thead>
		<tbody>
			{#each changes as c (c.path)}
				<tr>
					<th scope="row"><code>{c.path}</code></th>
					<td>{#if c.before !== undefined}<del>{show(c.before)}</del>{:else}<span class="muted">(none)</span>{/if}</td>
					<td>{#if c.after !== undefined}<ins>{show(c.after)}</ins>{:else}<span class="muted">(removed)</span>{/if}</td>
				</tr>
			{/each}
		</tbody>
	</table>
{/if}

<style>
	.diff {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	caption {
		text-align: left;
		font-weight: 600;
		padding-bottom: var(--space-2);
	}
	th,
	td {
		padding: var(--space-1) var(--space-2);
		border-bottom: 1px solid var(--color-border);
		text-align: left;
		vertical-align: top;
		overflow-wrap: anywhere;
	}
	thead th {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	del {
		color: var(--color-error);
	}
	ins {
		color: var(--color-ok);
		text-decoration: none;
		font-weight: 600;
	}
</style>
