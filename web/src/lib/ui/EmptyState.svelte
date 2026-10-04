<!-- Nothing to show yet: a title, a plain-language reason and optional actions (children). -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';
	import { icons } from './icons.ts';

	let { title, text = '', icon = icons.empty, children }: { title: string; text?: string; icon?: string; children?: Snippet } = $props();
</script>

<div class="empty">
	<span class="tile"><Icon d={icon} size={20} /></span>
	<p class="title">{title}</p>
	{#if text}<p class="muted">{text}</p>{/if}
	{#if children}<div class="actions">{@render children()}</div>{/if}
</div>

<style>
	.empty {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-6) var(--space-5);
		text-align: center;
		color: var(--color-text-muted);
		background: var(--color-inset);
		border: 1px dashed var(--color-border-strong);
		border-radius: var(--radius-lg);
	}
	.tile {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--tile-size-lg);
		height: var(--tile-size-lg);
		margin-bottom: var(--space-1);
		color: var(--color-accent);
		background: var(--color-accent-soft);
		border: 1px solid color-mix(in srgb, var(--color-accent) 25%, transparent);
		border-radius: var(--radius-md);
	}
	p {
		margin: 0;
		max-width: 36rem;
		font-size: var(--text-sm);
	}
	.title {
		font-weight: 600;
		color: var(--color-text);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: var(--space-2);
		margin-top: var(--space-2);
	}
</style>
