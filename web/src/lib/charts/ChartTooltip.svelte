<!--
	The crosshair card: when, the point's value with its status (shape and word) and the source
	chips behind it, the other series at that point, and the point's actions (Explain, Override,
	Raw records) once the card is pinned.
-->
<script lang="ts">
	import ResultStatus from '../components/ResultStatus.svelte';
	import Chip from '../ui/Chip.svelte';
	import { sourceClass } from '../ui/source.ts';
	import { providerName } from '../views/format.ts';
	import type { Tip } from './types.ts';

	let { tip, actions = [] }: { tip: Tip; actions?: { label: string; onclick: () => void }[] } = $props();
</script>

<div class="tip">
	<div class="head">
		<span>{tip.title}</span>
		{#if tip.lead?.status}<ResultStatus status={tip.lead.status} />{/if}
	</div>
	{#if tip.lead}
		<div class="lead">{tip.lead.value}{#if tip.lead.unit}<span class="unit">{tip.lead.unit}</span>{/if}</div>
		{#if tip.lead.providers?.length}
			<div class="chips">{#each tip.lead.providers as p (p)}<Chip source={p}>{providerName(p)}</Chip>{/each}</div>
		{/if}
	{/if}
	{#if tip.rows.length}
		<ul>
			{#each tip.rows as r, i (i)}
				<li class={[r.source && sourceClass(r.source)]}>
					<span class="label">{#if r.source}<span class="swatch" aria-hidden="true"></span>{/if}{r.label}</span>
					<strong>{r.value}</strong>
					{#if r.status}<span class="status"><ResultStatus status={r.status} /></span>{/if}
				</li>
			{/each}
		</ul>
	{/if}
	{#if tip.note}<div class="note">{tip.note}</div>{/if}
	{#if actions.length}
		<div class="actions">
			{#each actions as a (a.label)}<button class="btn link" type="button" onclick={a.onclick}>{a.label}</button>{/each}
		</div>
	{/if}
</div>

<style>
	.tip {
		display: grid;
		gap: var(--space-2);
		min-width: 12rem;
		max-width: 17rem;
		padding: var(--space-3) var(--space-4);
		font-size: var(--text-xs);
		background: var(--color-surface-2);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-2);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		gap: var(--space-1) var(--space-3);
		color: var(--color-text-muted);
	}
	.lead {
		font-size: var(--text-xl);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
		line-height: 1.1;
	}
	.unit {
		margin-left: var(--space-1);
		font-size: var(--text-sm);
		font-weight: 400;
		color: var(--color-text-muted);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
	}
	ul {
		display: grid;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
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
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.status {
		flex-basis: 100%;
	}
	.note {
		color: var(--color-text-muted);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		padding-top: var(--space-2);
		border-top: 1px solid var(--color-border);
	}
	.actions .btn {
		font-size: var(--text-xs);
		font-weight: 500;
	}
</style>
