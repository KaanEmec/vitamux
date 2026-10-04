<!-- Field-level diff of two rule specs (diffSpecs), side by side: one column per spec. -->
<script lang="ts">
	import { diffSpecs } from './rule.ts';

	let {
		before,
		after,
		caption,
		labels = ['Before', 'After']
	}: { before: unknown; after: unknown; caption: string; labels?: [string, string] } = $props();

	const changes = $derived(diffSpecs(before, after));
	const show = (v: unknown) => (v === undefined ? '' : JSON.stringify(v));
</script>

{#if changes.length === 0}
	<p class="muted">{caption}: no differences.</p>
{:else}
	<div class="scroll">
		<table class="diff">
			<caption>{caption}</caption>
			<thead><tr><th scope="col">Field</th><th scope="col">{labels[0]}</th><th scope="col">{labels[1]}</th></tr></thead>
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
	</div>
{/if}

<style>
	.scroll {
		overflow-x: auto;
	}
	.diff {
		width: 100%;
		table-layout: fixed;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	caption {
		padding-bottom: var(--space-2);
		font-weight: 600;
		text-align: left;
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		vertical-align: top;
		overflow-wrap: anywhere;
		border-bottom: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	td {
		font-family: var(--font-mono);
		font-size: var(--text-xs);
	}
	del {
		color: var(--color-text-muted);
	}
	ins {
		padding: 0 var(--space-1);
		font-weight: 600;
		color: var(--color-draft);
		text-decoration: none;
		background: var(--color-draft-bg);
		border-radius: var(--radius-xs);
	}
</style>
