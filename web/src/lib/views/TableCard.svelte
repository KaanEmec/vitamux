<!--
	A card with a heading and a table (children are the <thead> and <tbody>; give numeric cells
	class "num"). `remaining` rows beyond the shown ones offer a "Show more" button.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		remaining = 0,
		onmore,
		children
	}: { title: string; remaining?: number; onmore?: () => void; children: Snippet } = $props();

	const id = $props.id();
</script>

<section class="card" aria-labelledby={id}>
	<h2 id={id}>{title}</h2>
	<div class="scroll">
		<table>{@render children()}</table>
	</div>
	{#if remaining > 0}
		<p><button class="btn sm" type="button" onclick={onmore}>Show more ({remaining} left)</button></p>
	{/if}
</section>

<style>
	.card {
		margin-bottom: var(--space-4);
		padding-inline: 0;
	}
	h2,
	p {
		margin-inline: var(--space-5);
	}
	/* Positioned, so the visually hidden labels inside are clipped with the table. */
	.scroll {
		position: relative;
		overflow-x: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	.scroll :global(th),
	.scroll :global(td) {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		white-space: nowrap;
		border-top: 1px solid var(--color-border);
	}
	.scroll :global(thead th) {
		font-weight: 500;
		color: var(--color-text-muted);
		border-top: 0;
	}
	.scroll :global(tbody th) {
		font-weight: 400;
	}
	.scroll :global(th:first-child),
	.scroll :global(td:first-child) {
		padding-left: var(--space-5);
	}
	.scroll :global(.num) {
		text-align: right;
	}
</style>
