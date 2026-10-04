<!--
	The frame every x/y chart of the kit draws in: size, scales, grid and axes, a crosshair with
	the tooltip, keyboard focus per point (arrows, Home/End, PageUp/PageDown, Enter selects),
	brush-to-zoom, a polite live region, the table fallback and the "vx-chart-render" mark.
	Chart components pass `marks`, a snippet that draws with the frame's scales. Geometry is SVG
	attributes and CSSOM (style: directives): the CSP blocks inline style attributes.
-->
<script lang="ts" module>
	export interface Frame {
		sx: (v: number) => number;
		sy: (v: number) => number;
		left: number;
		right: number;
		top: number;
		bottom: number;
		/** Visible x domain (after zoom). */
		x: [number, number];
		/** Index of the focused or hovered point, or -1. */
		active: number;
	}
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import ChartTable from './ChartTable.svelte';
	import ChartTooltip from './ChartTooltip.svelte';
	import { formatNumber, linear, measureRender, nearest, ticks, timeTicks, type Domain } from './scale.ts';
	import { tipText, type TableData, type Tip } from './types.ts';

	let {
		label,
		xs,
		x,
		y,
		time = true,
		timezone,
		height = 280,
		zoom = false,
		tip,
		table,
		marks,
		legend,
		onselect,
		yFormat = (v: number) => formatNumber(v),
		yTicks: fixedTicks
	}: {
		/** Accessible name of the chart. */
		label: string;
		/** Sorted x of the points that the pointer and the keyboard visit. */
		xs: number[];
		x: Domain;
		y: Domain;
		time?: boolean;
		timezone?: string;
		height?: number;
		/** Drag across the plot to zoom into that range. */
		zoom?: boolean;
		tip: (i: number) => Tip;
		table: () => TableData;
		marks: Snippet<[Frame]>;
		legend?: Snippet;
		onselect?: (i: number) => void;
		yFormat?: (v: number) => string;
		/** Y tick values, when round numbers are not the right marks (a clock axis). */
		yTicks?: number[];
	} = $props();

	const margin = { top: 12, right: 12, bottom: 26, left: 48 };
	const clipId = $props.id();

	let width = $state(0);
	let active = $state(-1);
	let zoomed = $state<Domain | null>(null);
	let brush = $state<{ from: number; to: number } | null>(null);
	let announce = $state('');

	const xd = $derived(zoomed ?? x);
	const right = $derived(Math.max(width - margin.right, margin.left + 1));
	const bottom = $derived(height - margin.bottom);
	const sx = $derived(linear(xd, [margin.left, right]));
	const sy = $derived(linear(y, [bottom, margin.top]));
	const yTicks = $derived(fixedTicks ?? ticks(y, Math.max(2, Math.round((bottom - margin.top) / 56))));
	const xAxis = $derived.by(() => {
		const count = Math.max(2, Math.floor((right - margin.left) / 96));
		if (time) return timeTicks(xd, count, timezone);
		return { ticks: ticks(xd, count), format: (v: number) => formatNumber(v) };
	});
	/** Points inside the visible domain: [first, last] index. */
	const visible = $derived.by(() => {
		let a = xs.findIndex((v) => v >= xd[0]);
		if (a < 0) a = xs.length;
		let b = xs.length - 1;
		while (b >= a && xs[b] > xd[1]) b--;
		return [a, b] as const;
	});
	const frame = $derived<Frame>({ sx, sy, left: margin.left, right, top: margin.top, bottom, x: xd, active });
	const tipAt = $derived(active >= 0 && active < xs.length ? tip(active) : null);
	const tipLeft = $derived(active >= 0 ? sx(xs[active]) : 0);

	// "vx-chart-render": from receiving data to the next paint, once the width is known.
	let start = 0;
	$effect.pre(() => {
		void xs;
		start = performance.now();
	});
	$effect(() => {
		void xs;
		if (width > 0 && start) {
			measureRender(start);
			start = 0;
		}
	});

	const invert = (px: number) => xd[0] + ((px - margin.left) / (right - margin.left)) * (xd[1] - xd[0]);
	const plotX = (e: PointerEvent) => e.clientX - (e.currentTarget as Element).getBoundingClientRect().left;

	function pointerMove(e: PointerEvent) {
		const px = plotX(e);
		if (brush) brush.to = px;
		const i = nearest(xs, invert(px));
		active = i >= visible[0] && i <= visible[1] ? i : -1;
	}

	function pointerDown(e: PointerEvent) {
		if (!zoom || e.button !== 0) return;
		(e.currentTarget as Element).setPointerCapture(e.pointerId);
		brush = { from: plotX(e), to: plotX(e) };
	}

	function pointerUp() {
		const b = brush;
		brush = null;
		if (b && Math.abs(b.to - b.from) > 6) {
			const [a, c] = [invert(Math.min(b.from, b.to)), invert(Math.max(b.from, b.to))];
			zoomed = [Math.max(a, xd[0]), Math.min(c, xd[1])];
		} else if (active >= 0) onselect?.(active);
	}

	function move(i: number) {
		if (visible[1] < visible[0]) return;
		active = Math.min(Math.max(i, visible[0]), visible[1]);
		announce = tipText(tip(active));
	}

	function keydown(e: KeyboardEvent) {
		if (e.target !== e.currentTarget) return;
		const page = Math.max(1, Math.round((visible[1] - visible[0]) / 10));
		const keys: Record<string, () => void> = {
			ArrowRight: () => move(active + 1),
			ArrowLeft: () => move(active - 1),
			PageDown: () => move(active + page),
			PageUp: () => move(active - page),
			Home: () => move(visible[0]),
			End: () => move(visible[1]),
			Enter: () => active >= 0 && onselect?.(active),
			' ': () => active >= 0 && onselect?.(active),
			Escape: () => (zoomed ? (zoomed = null) : (active = -1))
		};
		if (!keys[e.key]) return;
		e.preventDefault();
		keys[e.key]();
	}
</script>

<div class="chart">
	{#if legend}<div class="legend">{@render legend()}</div>{/if}
	<!-- One tab stop: the arrows move between points (announced in the live region). -->
	<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
	<div
		class="plot"
		role="group"
		aria-roledescription="chart"
		aria-label="{label}. Arrow keys move between points{onselect ? ', Enter opens one' : ''}."
		tabindex="0"
		bind:clientWidth={width}
		onkeydown={keydown}
		onfocus={() => active < 0 && move(visible[1])}
		onblur={() => (active = -1)}
		onpointermove={pointerMove}
		onpointerleave={() => !brush && (active = -1)}
		onpointerdown={pointerDown}
		onpointerup={pointerUp}
	>
		{#if width > 0}
			<svg {width} {height} viewBox="0 0 {width} {height}" aria-hidden="true">
				<defs>
					<clipPath id="{clipId}-clip"><rect x={margin.left} y="0" width={right - margin.left} height={bottom + 1} /></clipPath>
				</defs>
				{#each yTicks as t (t)}
					<line class="grid" x1={margin.left} x2={right} y1={sy(t)} y2={sy(t)} />
					<text class="axis" x={margin.left - 8} y={sy(t)} dy="0.32em" text-anchor="end">{yFormat(t)}</text>
				{/each}
				{#each xAxis.ticks as t (t)}
					<text class="axis" x={sx(t)} y={height - 8} text-anchor="middle">{xAxis.format(t)}</text>
				{/each}
				<g clip-path="url(#{clipId}-clip)">{@render marks(frame)}</g>
				{#if active >= 0}<line class="crosshair" x1={tipLeft} x2={tipLeft} y1={margin.top} y2={bottom} />{/if}
				{#if brush}
					<rect class="brush" x={Math.min(brush.from, brush.to)} y={margin.top} width={Math.abs(brush.to - brush.from)} height={bottom - margin.top} />
				{/if}
			</svg>
			{#if tipAt && !brush}
				<div class={['tip', tipLeft > width / 2 && 'flip']} style:left="{tipLeft}px">
					<ChartTooltip tip={onselect ? { ...tipAt, note: tipAt.note ?? 'Click or press Enter for details' } : tipAt} />
				</div>
			{/if}
		{/if}
	</div>
	<p class="visually-hidden" aria-live="polite">{announce}</p>
	<div class="below">
		<ChartTable caption={label} data={table} />
		{#if zoomed}<button class="btn ghost sm" type="button" onclick={() => (zoomed = null)}>Reset zoom</button>{/if}
	</div>
</div>

<style>
	.chart {
		min-width: 0;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
		margin-bottom: var(--space-2);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.plot {
		position: relative;
		touch-action: pan-y;
		border-radius: var(--radius-sm);
	}
	svg {
		display: block;
		overflow: visible;
		user-select: none;
	}
	.grid {
		stroke: var(--chart-grid);
		shape-rendering: crispEdges;
	}
	.axis {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		fill: var(--chart-axis);
	}
	.crosshair {
		stroke: var(--color-text);
		stroke-opacity: 0.35;
		shape-rendering: crispEdges;
	}
	.brush {
		fill: var(--chart-band);
		stroke: var(--color-accent);
	}
	.tip {
		position: absolute;
		top: var(--space-2);
		z-index: 2;
		padding: 0 var(--space-3);
		pointer-events: none;
	}
	.tip.flip {
		transform: translateX(-100%);
	}
	.below {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-2);
	}
</style>
