<!--
	A table card for the connection tabs and lists: scrolls sideways on narrow screens (a keyboard
	stop, so it can be scrolled) and styles the th/td of the rows passed as children.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { label, children }: { label: string; children: Snippet } = $props();
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="scroll" role="region" aria-label={label} tabindex="0">
	<table>{@render children()}</table>
</div>

<style>
	.scroll {
		overflow-x: auto;
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-lg);
	}
	table {
		width: 100%;
		min-width: 36rem;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	.scroll :global(th),
	.scroll :global(td) {
		padding: var(--space-3) var(--space-4);
		text-align: left;
		vertical-align: top;
	}
	.scroll :global(thead th) {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	.scroll :global(tbody tr) {
		border-top: 1px solid var(--color-border);
	}
	.scroll :global(tbody th) {
		font-weight: 600;
	}
	.scroll :global(table table) {
		min-width: 0;
	}
</style>
