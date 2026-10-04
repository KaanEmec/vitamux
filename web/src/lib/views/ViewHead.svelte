<!-- Header of a specialised view: breadcrumb back to Explore, the metric tile and h1, a plain description and actions (a range picker). -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import MetricTile from '../ui/MetricTile.svelte';

	let {
		title,
		text,
		tile,
		children
	}: { title: string; text: string; /** Metric (and catalogue section) whose icon tile and hue lead the title. */ tile?: { code: string; section?: string }; children?: Snippet } = $props();
</script>

<nav aria-label="Breadcrumb" class="crumbs">
	<a href="/explore">Explore</a><span aria-hidden="true">/</span><span aria-current="page">{title}</span>
</nav>
<header>
	{#if tile}<MetricTile code={tile.code} section={tile.section} size="lg" />{/if}
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
		align-items: center;
		gap: var(--space-3) var(--space-4);
		margin-bottom: var(--space-5);
	}
	.titles {
		flex: 1 1 16rem;
	}
	h1 {
		margin: 0;
		letter-spacing: var(--tracking-tight);
	}
	p {
		margin: var(--space-1) 0 0;
	}
</style>
