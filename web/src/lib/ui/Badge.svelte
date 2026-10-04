<!-- A short label: neutral (tags such as "v3 · custom"), accent ("Official"), warn ("Unofficial"), draft. -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { tone = 'neutral', title, children }: { tone?: 'neutral' | 'accent' | 'warn' | 'draft'; title?: string; children: Snippet } = $props();
</script>

<span class={['badge', tone]} {title}>{@render children()}</span>

<style>
	.badge {
		display: inline-flex;
		align-items: center;
		min-height: 1.5rem;
		padding: 0 var(--space-2);
		font-size: var(--text-xs);
		font-weight: 600;
		white-space: nowrap;
		border: 1px solid transparent;
		border-radius: var(--radius-xs);
	}
	.neutral {
		font-weight: 500;
		color: var(--color-text-muted);
		background: var(--color-surface-2);
	}
	.accent {
		color: var(--color-link);
		border-color: color-mix(in srgb, var(--color-accent) 40%, transparent);
	}
	.warn {
		color: var(--color-warn);
		border-color: color-mix(in srgb, var(--color-warn) 45%, transparent);
	}
	.draft {
		color: var(--color-draft);
		background: var(--color-draft-bg);
	}
</style>
