<!-- A row of plain statistics: a label, a value and an optional unit. -->
<script lang="ts">
	let { items }: { items: { k: string; v: string; u?: string }[] } = $props();
</script>

<dl>
	{#each items as s (s.k)}
		<div class="card">
			<dt>{s.k}</dt>
			<dd>{s.v}{#if s.u} <span class="unit">{s.u}</span>{/if}</dd>
		</div>
	{/each}
</dl>

<style>
	dl {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(10.5rem, 1fr));
		gap: var(--space-3);
		margin: 0 0 var(--space-5);
	}
	.card {
		padding: var(--space-3) var(--space-4);
	}
	dt {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	dd {
		margin: var(--space-1) 0 0;
		font-size: var(--text-xl);
		font-weight: 600;
	}
	.unit {
		font-size: var(--text-sm);
		font-weight: 400;
		color: var(--color-text-muted);
	}
</style>
