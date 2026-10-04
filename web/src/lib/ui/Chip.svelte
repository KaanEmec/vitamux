<!-- A source chip: the source's colour dot and a label. `dashed` marks a source that was not selected. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { sourceClass } from './source.ts';

	let { source, dashed = false, children }: { source?: string; dashed?: boolean; children: Snippet } = $props();
</script>

<span class={['chip', source && sourceClass(source), dashed && 'dashed']}>
	{#if source}<span class="dot" aria-hidden="true"></span>{/if}
	{@render children()}
</span>

<style>
	/* A source chip is tinted with its source colour; without a source it is neutral. */
	.chip {
		--tone: var(--src, var(--color-neutral));
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		min-height: 1.5rem;
		padding: 0 0.625rem;
		font-size: var(--text-xs);
		font-weight: 500;
		white-space: nowrap;
		color: color-mix(in srgb, var(--tone) 55%, var(--color-text));
		background: color-mix(in srgb, var(--tone) 10%, var(--color-surface));
		border: 1px solid color-mix(in srgb, var(--tone) 32%, var(--color-surface));
		border-radius: var(--radius-pill);
	}
	.dashed {
		color: var(--color-text-muted);
		background: transparent;
		border-style: dashed;
		border-color: var(--color-border-strong);
	}
	.dot {
		width: 0.375rem;
		height: 0.375rem;
		background: var(--tone);
		border-radius: 50%;
	}
</style>
