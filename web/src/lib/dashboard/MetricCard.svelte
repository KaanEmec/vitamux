<!--
	One pinned dashboard card: the metric's tile and source, its value for the day with status, a
	neutral delta against the 30-day mean and a sparkline in the metric hue. The title links to the metric (a stretched
	link, so the whole card opens it); with `tools` the card is being edited and does not link.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLAttributes } from 'svelte/elements';
	import ResultStatus from '../components/ResultStatus.svelte';
	import { metricHref } from '../nav.ts';
	import Chip from '../ui/Chip.svelte';
	import { metricLook } from '../ui/metric.ts';
	import MetricTile from '../ui/MetricTile.svelte';
	import { hoursMinutes, type CardView } from './summary.ts';

	let {
		code,
		section,
		label,
		size,
		view,
		date,
		edit = false,
		over = false,
		tools,
		...rest
	}: {
		code: string;
		/** Catalogue section (GET /metrics), which picks the hue and icon. */
		section?: string;
		label: string;
		size: 'S' | 'M' | 'L';
		view: CardView | null;
		date?: string;
		edit?: boolean;
		/** Edit mode: a dragged card is above this one. */
		over?: boolean;
		tools?: Snippet;
	} & HTMLAttributes<HTMLElement> = $props();

	// The kit's sparkline and stage stack are tiny, but they stay lazy chunks like the other charts.
	const kit = import('../charts/Sparkline.svelte');
	const stack = import('../charts/StageStack.svelte');
	const titleId = $props.id();
	const chip = $derived(view?.chips[0]);
	const more = $derived(view?.chips.slice(1, 3) ?? []);
</script>

<article
	{...rest}
	class="card"
	class:m={size === 'M'}
	class:l={size === 'L'}
	class:edit
	class:over
	style:--metric={metricLook(code, section).color}
	aria-labelledby={titleId}
>
	{#if edit}{@render tools?.()}{/if}
	<header>
		<MetricTile {code} {section} size="sm" />
		<h3 id={titleId}>
			{#if edit}{label}{:else}<a href={metricHref(code)}>{label}</a>{/if}
		</h3>
		{#if chip}<Chip source={chip.provider}>{chip.label}</Chip>{/if}
	</header>

	{#if !view}
		<p class="value muted" role="status">Loading…</p>
	{:else}
		<div class="reading">
			<p class="value">{view.value}{#if view.unit}<span class="unit">{view.unit}</span>{/if}</p>
			<span class="status"><ResultStatus status={view.status} partial={view.partial} /></span>
		</div>
		{#if view.sub}<p class="sub">{view.sub}</p>{/if}

		{#if view.stages}
			{#await stack then { default: StageStack }}
				<StageStack stages={view.stages} label="Time in each sleep stage" format={hoursMinutes} />
			{/await}
		{/if}

		{#if view.ys.some((y) => y != null)}
			<div class="spark">
				{#await kit then { default: Sparkline }}
					<Sparkline ys={view.ys} bars={view.bars} band={view.band} mean={view.mean} />
				{/await}
			</div>
		{/if}

		<footer>
			{#if view.delta}<span class="delta">{view.delta}</span>{/if}
			{#each more as c (c.label)}<Chip source={c.provider}>{c.label}</Chip>{/each}
			{#if view.chips.length > more.length + 1}<span class="muted">+{view.chips.length - more.length - 1}</span>{/if}
			{#if !edit && view.hasData && view.status !== 'no_data' && date}
				<a class="all" href="/explore/{code}/day/{date}">All sources<span class="visually-hidden"> for {label}</span></a>
			{/if}
		</footer>
	{/if}
</article>

<style>
	article {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		min-width: 0;
		min-height: 11.5rem;
		padding: var(--space-4);
	}
	@media (min-width: 36rem) {
		.m,
		.l {
			grid-column: span 2;
		}
	}
	.edit {
		padding-top: var(--space-3);
	}
	.over {
		border-style: dashed;
		border-color: var(--color-accent);
	}
	header {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	h3 {
		flex: 1;
		min-width: 0;
		margin: 0;
		font-size: var(--text-sm);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	h3 a {
		color: inherit;
		text-decoration: none;
	}
	/* The title link covers the card; the "All sources" link sits above it. */
	h3 a::after {
		content: '';
		position: absolute;
		inset: 0;
		border-radius: inherit;
	}
	article:has(h3 a:hover) {
		border-color: color-mix(in srgb, var(--metric) 55%, var(--color-border));
	}
	h3 a:focus-visible {
		outline: none;
	}
	article:has(h3 a:focus-visible) {
		outline: 2px solid var(--color-focus);
		outline-offset: 2px;
	}
	.reading {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
	}
	.status {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	p {
		margin: 0;
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
	.delta {
		font-variant-numeric: tabular-nums;
	}
	.unit,
	.sub {
		font-size: var(--text-sm);
		font-weight: 400;
		color: var(--color-text-muted);
	}
	.spark {
		margin-top: auto;
	}
	.l .spark :global(.spark) {
		height: 5rem;
	}
	footer {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		margin-top: auto;
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.all {
		position: relative;
		z-index: 1;
		margin-left: auto;
	}
</style>
