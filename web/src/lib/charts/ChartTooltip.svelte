<!-- Tooltip content: when, each series' value with its source and status, and a note. -->
<script lang="ts">
	import ResultStatus from '../components/ResultStatus.svelte';
	import { sourceClass } from '../ui/source.ts';
	import type { Tip } from './types.ts';

	let { tip }: { tip: Tip } = $props();
</script>

<div class="tip">
	<div class="title">{tip.title}</div>
	{#each tip.rows as r, i (i)}
		<div class={['row', r.source && sourceClass(r.source)]}>
			<span class="label">{#if r.source}<span class="swatch" aria-hidden="true"></span>{/if}{r.label}</span>
			<strong>{r.value}</strong>
			{#if r.status}<span class="status"><ResultStatus status={r.status} /></span>{/if}
		</div>
	{/each}
	{#if tip.note}<div class="note">{tip.note}</div>{/if}
</div>

<style>
	.tip {
		display: grid;
		gap: var(--space-1);
		min-width: 12rem;
		max-width: 18rem;
		padding: var(--space-3);
		font-size: var(--text-xs);
		background: var(--color-bg);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-md);
		box-shadow: var(--shadow-2);
	}
	.title {
		color: var(--color-text-muted);
	}
	.row {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 var(--space-2);
	}
	.label {
		display: inline-flex;
		flex: 1;
		align-items: center;
		gap: var(--space-1);
		color: var(--color-text-muted);
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		background: var(--src);
		border-radius: 50%;
	}
	strong {
		font-size: var(--text-md);
		font-weight: 600;
	}
	.status {
		flex-basis: 100%;
	}
	.note {
		color: var(--color-link);
	}
</style>
