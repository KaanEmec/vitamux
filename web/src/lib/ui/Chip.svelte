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
	.chip {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-height: 1.625rem;
		padding: 0 var(--space-3);
		font-size: var(--text-xs);
		white-space: nowrap;
		background: var(--color-surface-2);
		border: 1px solid transparent;
		border-radius: var(--radius-pill);
	}
	.dashed {
		color: var(--color-text-muted);
		background: transparent;
		border: 1px dashed var(--color-border-strong);
	}
	.dot {
		width: 0.4375rem;
		height: 0.4375rem;
		background: var(--src, var(--color-neutral));
		border-radius: 50%;
	}
</style>
