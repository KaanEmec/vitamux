<!--
	The frame every x/y chart of the kit draws in (ADR-0022): LayerChart's <ChartCore> and <Svg>
	(lighter than <Chart>, whose extra marks the kit never uses) with our scales' domains, grid and
	axes (timezone-aware ticks), animated domain changes (none under reduced motion), and one
	interaction model on top: pointer, touch scrub and keyboard all move
	one active point (arrows, Home/End, PageUp/PageDown, Enter opens it), shown by a crosshair and
	the ChartTooltip card. A click pins the card so its actions can be used. Drag zooms (`view`,
	shared with a BrushNavigator). Also a polite live region, the table fallback and the
	"vx-chart-render" User Timing mark. Chart components pass `marks`, drawn with the frame's
	scales in plot coordinates. Geometry is SVG attributes and CSSOM: the CSP blocks inline styles.
-->
<script lang="ts" module>
	export interface Frame {
		/** Scales from data to plot coordinates (0 is the plot's left and top). */
		sx: (v: number) => number;
		sy: (v: number) => number;
		left: number;
		right: number;
		top: number;
		bottom: number;
		/** Index of the focused, hovered or pinned point, or -1. */
		active: number;
	}
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';
	import { prefersReducedMotion } from 'svelte/motion';
	import { Axis, ChartCore, Svg } from 'layerchart/svg';
	import ChartTable from './ChartTable.svelte';
	import ChartTooltip from './ChartTooltip.svelte';
	import { formatNumber, linear, measureRender, nearest, ticks, timeTicks, type Domain } from './scale.ts';
	import { tipText, type TableData, type Tip, type TipAction } from './types.ts';

	let {
		label,
		xs,
		x,
		y,
		time = true,
		timezone,
		height = 280,
		zoom = false,
		view = $bindable(null),
		padding,
		crosshair = true,
		pick,
		tip,
		table,
		marks: draw,
		legend,
		actions = [],
		onselect,
		yFormat = (v: number) => formatNumber(v),
		yTicks: fixedTicks
	}: {
		/** Accessible name of the chart. */
		label: string;
		/** x of the points that the pointer and the keyboard visit, ascending. */
		xs: number[];
		x: Domain;
		y: Domain;
		time?: boolean;
		timezone?: string;
		height?: number;
		/** Drag across the plot to zoom into that range. */
		zoom?: boolean;
		/** The zoomed x domain, or null for all of `x` (bind it to a BrushNavigator). */
		view?: Domain | null;
		/** Plot margins, for wide axis labels. */
		padding?: Partial<Record<'top' | 'right' | 'bottom' | 'left', number>>;
		/** Draw the vertical crosshair (charts of intervals highlight the interval instead). */
		crosshair?: boolean;
		/** The point under an x value (default: the nearest of `xs`). */
		pick?: (t: number) => number;
		tip: (i: number) => Tip;
		table: () => TableData;
		marks: Snippet<[Frame]>;
		legend?: Snippet;
		/** Point actions, offered in the pinned tooltip; the first is also Enter's without `onselect`. */
		actions?: TipAction[];
		onselect?: (i: number) => void;
		yFormat?: (v: number) => string;
		/** Y tick values, when round numbers are not the right marks (a clock axis). */
		yTicks?: number[];
	} = $props();

	let width = $state(0);
	let active = $state(-1);
	let pinned = $state(false);
	let brush = $state<{ from: number; to: number } | null>(null);
	let announce = $state('');
	let plot = $state<HTMLElement>();
	let touch: { x: number; moved: boolean } | null = null;

	const clipId = $props.id();
	// Read once: LayerChart sets up its domain motion when the chart is created.
	const motion = prefersReducedMotion.current ? undefined : ({ type: 'tween', duration: 300 } as const);
	const m = $derived({ top: 12, right: 12, bottom: 26, left: 48, ...padding });
	const xd = $derived(view ?? x);
	const plotW = $derived(Math.max(width - m.left - m.right, 1));
	const sx = $derived(linear(xd, [0, plotW]));
	const yTicks = $derived(fixedTicks ?? ticks(y, Math.max(2, Math.round((height - m.top - m.bottom) / 56))));
	const xAxis = $derived.by(() => {
		const count = Math.max(2, Math.floor(plotW / 96));
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
	const tipAt = $derived(active >= 0 && active < xs.length ? tip(active) : null);
	const tipLeft = $derived(active >= 0 ? m.left + sx(xs[active]) : 0);
	const hint = $derived(actions.length ? 'Click to pin, Enter to open' : onselect ? 'Click or press Enter for details' : undefined);

	// "vx-chart-render": from receiving data to the next paint, once the width is known. New
	// points (a zoom that loads finer buckets) drop the active one: its index means another point.
	let start = 0;
	$effect.pre(() => {
		void xs;
		start = performance.now();
		active = -1;
		pinned = false;
	});
	$effect(() => {
		void xs;
		if (width > 0 && start) {
			measureRender(start);
			start = 0;
		}
	});

	const plotX = (e: PointerEvent) => e.clientX - (plot?.getBoundingClientRect().left ?? 0) - m.left;
	const at = (px: number) => xd[0] + (px / plotW) * (xd[1] - xd[0]);
	// With motion, LayerChart sets the first domain in an effect: draw once the scales have one.
	const ready = (c: { xDomain: unknown[]; yDomain: unknown[] }) => c.xDomain.length === 2 && c.yDomain.length === 2;
	const inTip = (e: Event) => !!(e.target as Element | null)?.closest?.('.tip-at');

	function hover(px: number) {
		const i = pick ? pick(at(px)) : nearest(xs, at(px));
		active = i >= visible[0] && i <= visible[1] ? i : -1;
	}

	function pointerMove(e: PointerEvent) {
		if (pinned || inTip(e)) return;
		const px = plotX(e);
		if (brush) brush.to = px;
		if (touch && Math.abs(e.clientX - touch.x) > 6) touch.moved = true;
		hover(px);
	}

	function pointerDown(e: PointerEvent) {
		if (inTip(e) || e.button !== 0) return;
		pinned = false;
		if (e.pointerType === 'touch') {
			// Touch scrubs through the points; a tap selects like a click.
			touch = { x: e.clientX, moved: false };
			hover(plotX(e));
		} else if (zoom) {
			plot?.setPointerCapture(e.pointerId);
			brush = { from: plotX(e), to: plotX(e) };
		}
	}

	function pointerUp(e: PointerEvent) {
		if (inTip(e)) return;
		const b = brush;
		const t = touch;
		brush = null;
		touch = null;
		if (b && Math.abs(b.to - b.from) > 6) {
			const [lo, hi] = [at(Math.min(b.from, b.to)), at(Math.max(b.from, b.to))];
			view = [Math.max(lo, xd[0]), Math.min(hi, xd[1])];
		} else if (active >= 0 && !t?.moved) choose(active);
	}

	function choose(i: number) {
		if (actions.length) pinned = true;
		else onselect?.(i);
	}

	function move(i: number) {
		if (visible[1] < visible[0]) return;
		pinned = false;
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
			Enter: () => active >= 0 && (onselect ?? actions[0]?.run)?.(active),
			' ': () => active >= 0 && (onselect ?? actions[0]?.run)?.(active),
			Escape: () => (pinned ? (pinned = false) : view ? (view = null) : (active = -1))
		};
		if (!keys[e.key]) return;
		e.preventDefault();
		keys[e.key]();
	}

	function focusOut(e: FocusEvent) {
		if (plot?.contains(e.relatedTarget as Node | null)) return;
		pinned = false;
		active = -1;
	}

	function leave(e: PointerEvent) {
		if (!brush && !pinned && e.pointerType !== 'touch') active = -1;
	}

	// A pinned card closes when the pointer goes down anywhere else.
	function outside(e: PointerEvent) {
		if (pinned && !plot?.contains(e.target as Node)) {
			pinned = false;
			active = -1;
		}
	}
</script>

<svelte:window onpointerdown={outside} />

<div class="chart">
	{#if legend}<div class="legend">{@render legend()}</div>{/if}
	<!-- One tab stop: the arrows move between points (announced in the live region). -->
	<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
	<div
		class={['plot', zoom && 'zoom']}
		role="group"
		aria-roledescription="chart"
		aria-label="{label}. Arrow keys move between points{onselect || actions.length ? ', Enter opens one' : ''}."
		tabindex="0"
		bind:this={plot}
		bind:clientWidth={width}
		onkeydown={keydown}
		onfocus={() => active < 0 && move(visible[1])}
		onfocusout={focusOut}
		onpointermove={pointerMove}
		onpointerleave={leave}
		onpointerdown={pointerDown}
		onpointerup={pointerUp}
		onpointercancel={() => {
			touch = null;
			brush = null;
		}}
	>
		{#if width > 0}
			<div class="canvas" aria-hidden="true">
				<ChartCore {width} {height} xDomain={xd} yDomain={y} padding={m} {motion}>
					{#snippet children({ context })}
						<Svg>
							{#if ready(context)}
								{#each yTicks as t (t)}
									<line class="grid" x1="0" x2={context.width} y1={context.yScale(t)} y2={context.yScale(t)} />
								{/each}
								<clipPath id="{clipId}-clip"><rect x="-1" y="-6" width={context.width + 2} height={context.height + 7} /></clipPath>
								<g clip-path="url(#{clipId}-clip)">
									{@render draw({ sx: context.xScale, sy: context.yScale, left: 0, right: context.width, top: 0, bottom: context.height, active })}
								</g>
								<Axis placement="left" ticks={yTicks} format={yFormat} tickMarks={false} classes={{ tickLabel: 'axis' }} />
								<Axis placement="bottom" ticks={xAxis.ticks} format={xAxis.format} tickMarks={false} classes={{ tickLabel: 'axis' }} />
								{#if crosshair && active >= 0}
									{@const cx = context.xScale(xs[active])}
									<line class="crosshair" x1={cx} x2={cx} y1="0" y2={context.height} />
								{/if}
								{#if brush}
									<rect class="brush" x={Math.min(brush.from, brush.to)} y="0" width={Math.abs(brush.to - brush.from)} height={context.height} />
								{/if}
							{/if}
						</Svg>
					{/snippet}
				</ChartCore>
			</div>
			{#if tipAt && !brush}
				<div class={['tip-at', tipLeft > width / 2 && 'flip', pinned && 'pinned']} style:left="{tipLeft}px">
					<ChartTooltip
						tip={pinned || !hint ? tipAt : { ...tipAt, note: tipAt.note ?? hint }}
						actions={pinned ? actions.map((a) => ({ label: a.label, onclick: () => a.run(active) })) : []}
					/>
				</div>
			{/if}
		{/if}
	</div>
	<p class="visually-hidden" aria-live="polite">{announce}</p>
	<div class="below">
		<ChartTable caption={label} data={table} />
		{#if view}<button class="btn ghost sm" type="button" onclick={() => (view = null)}>Reset zoom</button>{/if}
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
	.plot.zoom {
		cursor: crosshair;
	}
	.canvas :global(svg) {
		overflow: visible;
	}
	.canvas :global(.grid) {
		stroke: var(--chart-grid);
		shape-rendering: crispEdges;
	}
	.canvas :global(.axis) {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		fill: var(--chart-axis);
		stroke: none;
	}
	.canvas :global(.lc-axis-rule) {
		display: none;
	}
	.canvas :global(.crosshair) {
		stroke: var(--color-text);
		stroke-opacity: 0.35;
		shape-rendering: crispEdges;
	}
	.canvas :global(.brush) {
		fill: var(--chart-band);
		stroke: var(--color-accent);
	}
	.tip-at {
		position: absolute;
		top: var(--space-2);
		z-index: 2;
		padding: 0 var(--space-3);
		pointer-events: none;
	}
	.tip-at.flip {
		transform: translateX(-100%);
	}
	.tip-at.pinned {
		pointer-events: auto;
	}
	.below {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-2);
	}
</style>
