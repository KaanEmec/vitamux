<!--
	The Settings card: a titled section with an optional description, optional header actions
	(`aside`) and the body (form, table or text). Every Settings page is a stack of these.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		description = '',
		id,
		aside,
		children
	}: { title: string; description?: string; id?: string; aside?: Snippet; children: Snippet } = $props();

	const uid = $props.id();
	const headingId = $derived(id ?? uid);
</script>

<section class="card" aria-labelledby={headingId}>
	<header>
		<div>
			<h3 id={headingId}>{title}</h3>
			{#if description}<p class="muted">{description}</p>{/if}
		</div>
		{#if aside}<div class="aside">{@render aside()}</div>{/if}
	</header>
	{@render children()}
</section>

<style>
	header {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: flex-start;
		justify-content: space-between;
		margin-bottom: var(--space-4);
	}
	h3 {
		margin-bottom: var(--space-1);
		font-size: var(--text-lg);
	}
	p {
		max-width: 46rem;
		margin: 0;
		font-size: var(--text-sm);
	}
	.aside {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
</style>
