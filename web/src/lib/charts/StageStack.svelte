<!--
	One night's (or the average night's) time in each sleep stage as a single proportional bar,
	deep to awake, with a legend that gives every stage's time in words. Plain HTML (no axes or
	points), so it stays tiny; widths are CSSOM (style: directives), never inline style attributes.
-->
<script lang="ts">
	import { stageColor, stageLabels } from './sleep.ts';

	let {
		stages,
		label,
		format = (s: number) => `${Math.floor(s / 3600)}h ${String(Math.round((s % 3600) / 60)).padStart(2, '0')}m`,
		legend = true
	}: {
		/** Seconds per stage, in display order. */
		stages: { stage: string; seconds: number; label?: string }[];
		/** Accessible name of the bar. */
		label: string;
		format?: (seconds: number) => string;
		legend?: boolean;
	} = $props();

	const shown = $derived(stages.filter((s) => s.seconds > 0));
	const name = (s: { stage: string; label?: string }) => s.label ?? stageLabels[s.stage] ?? s.stage;
</script>

<div class="stack" role="img" aria-label={label}>
	{#each shown as s (s.stage)}<span class={['seg', stageColor(s.stage)]} style:flex-grow={s.seconds}></span>{/each}
</div>
{#if legend}
	<ul class="legend">
		{#each shown as s (s.stage)}
			<li><span class={['swatch', stageColor(s.stage)]}></span>{name(s)} <b>{format(s.seconds)}</b></li>
		{/each}
	</ul>
{/if}

<style>
	.stack {
		display: flex;
		gap: 2px;
		height: 0.625rem;
		overflow: hidden;
		border-radius: var(--radius-pill);
	}
	.seg {
		flex-basis: 0;
		min-width: 2px;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		margin: var(--space-2) 0 0;
		padding: 0;
		font-size: var(--text-xs);
		color: var(--color-text-muted);
		list-style: none;
	}
	.legend li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
	}
	.legend b {
		font-weight: 500;
		color: var(--color-text);
		font-variant-numeric: tabular-nums;
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
	}
	.deep {
		background: var(--stage-deep);
	}
	.light {
		background: var(--stage-light);
	}
	.rem {
		background: var(--stage-rem);
	}
	.awake {
		background: var(--stage-awake);
	}
	.other {
		background: var(--stage-other);
	}
</style>
