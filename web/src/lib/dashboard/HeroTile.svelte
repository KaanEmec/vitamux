<!--
	A hero stat tile: the metric's tile, a sparkline, the latest value and the period mean. It is a
	toggle button; the pressed tile drives the hero chart and takes a metric-hue border and glow.
-->
<script lang="ts">
	import { metricLook } from '../ui/metric.ts';
	import MetricTile from '../ui/MetricTile.svelte';
	import type { TileView } from './summary.ts';

	let {
		code,
		section,
		label,
		view,
		pressed,
		onclick
	}: { code: string; section?: string; label: string; view: TileView; pressed: boolean; onclick: () => void } = $props();

	const kit = import('../charts/Sparkline.svelte');
</script>

<button type="button" class="tile" aria-pressed={pressed} style:--metric={metricLook(code, section).color} {onclick}>
	<span class="top">
		<MetricTile {code} {section} />
		{#if view.ys.some((y) => y != null)}
			<span class="spark">
				{#await kit then { default: Sparkline }}<Sparkline ys={view.ys} bars={view.bars} />{/await}
			</span>
		{/if}
	</span>
	<span class="value">{view.value}{#if view.unit}<span class="unit">{view.unit}</span>{/if}</span>
	<span class="foot"><span class="label">{label}</span>{#if view.sub}<span class="sub">{view.sub}</span>{/if}</span>
</button>

<style>
	.tile {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
		padding: var(--space-4);
		font: inherit;
		text-align: left;
		color: var(--color-text);
		background: var(--card-bg);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-lg);
		cursor: pointer;
		transition:
			border-color 0.2s,
			box-shadow 0.2s;
	}
	.tile:hover {
		border-color: color-mix(in srgb, var(--metric) 45%, var(--color-border));
	}
	.tile[aria-pressed='true'] {
		border-color: var(--metric);
		box-shadow:
			0 0 0 3px color-mix(in srgb, var(--metric) 14%, transparent),
			0 0 2rem color-mix(in srgb, var(--metric) 18%, transparent);
	}
	@media (prefers-reduced-motion: reduce) {
		.tile {
			transition: none;
		}
	}
	.top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.spark {
		width: 6rem;
	}
	.spark :global(.spark) {
		height: 1.75rem;
	}
	.value {
		display: flex;
		align-items: baseline;
		gap: var(--space-2);
		font-size: var(--text-2xl);
		font-weight: 600;
		letter-spacing: var(--tracking-tight);
		font-variant-numeric: tabular-nums;
	}
	.unit,
	.foot {
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: normal;
		color: var(--color-text-muted);
	}
	.foot {
		display: flex;
		flex-wrap: wrap;
		justify-content: space-between;
		gap: var(--space-1) var(--space-2);
		font-size: var(--text-xs);
	}
	.sub {
		font-variant-numeric: tabular-nums;
	}
</style>
