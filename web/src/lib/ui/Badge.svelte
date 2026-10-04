<!-- A short tinted label: neutral (tags such as "v3 · custom"), accent ("Official"), warn ("Unofficial"), error, info, draft. -->
<script lang="ts">
	import type { Snippet } from 'svelte';

	let { tone = 'neutral', title, children }: { tone?: 'neutral' | 'accent' | 'warn' | 'error' | 'info' | 'draft'; title?: string; children: Snippet } = $props();
</script>

<span class={['badge', tone]} {title}>{@render children()}</span>

<style>
	.badge {
		--tone: var(--color-text-muted);
		display: inline-flex;
		align-items: center;
		min-height: 1.375rem;
		padding: 0 var(--space-2);
		font-size: var(--text-xs);
		font-weight: 600;
		line-height: 1;
		white-space: nowrap;
		color: var(--tone);
		background: var(--tone-bg, transparent);
		border: 1px solid color-mix(in srgb, var(--tone) 35%, transparent);
		border-radius: var(--radius-xs);
	}
	.neutral {
		--tone-bg: var(--color-surface-2);
		font-weight: 500;
		border-color: var(--color-border);
	}
	.accent {
		--tone: var(--color-link);
		--tone-bg: var(--color-accent-soft);
	}
	.warn {
		--tone: var(--color-warn);
		--tone-bg: var(--color-warn-bg);
	}
	.error {
		--tone: var(--color-error);
		--tone-bg: var(--color-error-bg);
	}
	.info {
		--tone: var(--color-info);
		--tone-bg: var(--color-info-bg);
	}
	.draft {
		--tone: var(--color-draft);
		--tone-bg: var(--color-draft-bg);
	}
</style>
