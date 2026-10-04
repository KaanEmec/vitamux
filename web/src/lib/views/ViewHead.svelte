<!-- Header of a specialised view: breadcrumb back to Explore, the h1, a plain description and actions (a range picker). -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { title, text, children }: { title: string; text: string; children?: Snippet } = $props();
</script>

<nav aria-label="Breadcrumb" class="crumbs">
	<a href="/explore">Explore</a><span aria-hidden="true">/</span><span aria-current="page">{title}</span>
</nav>
<header>
	<div class="titles">
		<h1>{title}</h1>
		<p class="muted">{text}</p>
	</div>
	{#if children}<div class="actions">{@render children()}</div>{/if}
</header>

<style>
	.crumbs {
		display: flex;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.crumbs a {
		text-decoration: none;
	}
	[aria-current] {
		color: var(--color-text);
	}
	header {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}
	.titles {
		flex: 1 1 20rem;
	}
	h1 {
		margin: 0;
	}
	p {
		margin: var(--space-1) 0 0;
	}
</style>
