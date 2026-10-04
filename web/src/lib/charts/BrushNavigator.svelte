<!--
	A small overview of the whole range under a chart, with a window to drag (LayerChart's brush).
	It shares `view` with the chart above it (bind:view on both): dragging or resizing the window
	zooms the chart, and zooming the chart moves the window. The wheel zooms around the pointer.
	One tab stop: arrows move the window, + and - zoom, Home/End go to either end, Escape shows all.
-->
<script lang="ts">
	import { Area, ChartCore, Spline, Svg } from 'layerchart/svg';
	import { decimate, extent, formatInstant, type Domain, type Row } from './scale.ts';

	let {
		xs,
		ys,
		view = $bindable(null),
		label,
		timezone,
		withTime = false
	}: {
		xs: number[];
		ys: (number | null)[];
		view?: Domain | null;
		label: string;
		timezone?: string;
		/** Label the window with clock times (a day's navigator). */
		withTime?: boolean;
	} = $props();

	type Brush = { x: unknown[]; active: boolean | undefined };
	let context = $state<{ brushState?: Brush }>();
	let width = $state(0);

	const full = $derived(extent(xs));
	const rows = $derived(decimate(xs, ys, full, 400));
	const y = $derived(extent(ys, 0.1));
	const defined = (d: Row) => d.y != null;
	const fmt = (t: number) => formatInstant(t, timezone, withTime);
	const span = $derived(full[1] - full[0]);

	// The chart above zoomed or reset: move the window to match.
	$effect(() => {
		const b = context?.brushState;
		if (!b) return;
		const [a, c] = (b.x ?? []) as number[];
		if (view && (a !== view[0] || c !== view[1])) {
			b.x = [...view];
			b.active = true;
		} else if (!view && b.active) {
			b.x = [null, null];
			b.active = false;
		}
	});

	function set(lo: number, hi: number) {
		const w = Math.min(hi - lo, span);
		if (w >= span * 0.999) return void (view = null);
		const a = Math.min(Math.max(lo, full[0]), full[1] - w);
		view = [a, a + w];
	}

	function onchange({ brush }: { brush: Brush }) {
		const [a, c] = brush.x as (number | null)[];
		view = brush.active && a != null && c != null && c > a ? [a, c] : null;
	}

	function keydown(e: KeyboardEvent) {
		const [a, c] = view ?? full;
		const w = c - a;
		const keys: Record<string, () => void> = {
			ArrowLeft: () => set(a - w / 4, c - w / 4),
			ArrowRight: () => set(a + w / 4, c + w / 4),
			'+': () => set(a + w / 4, c - w / 4),
			'=': () => set(a + w / 4, c - w / 4),
			'-': () => set(a - w / 2, c + w / 2),
			Home: () => set(full[0], full[0] + w),
			End: () => set(full[1] - w, full[1]),
			Escape: () => (view = null)
		};
		if (!keys[e.key]) return;
		e.preventDefault();
		keys[e.key]();
	}

	// Not passive: the wheel zooms the window instead of scrolling the page.
	const wheelZoom = (node: HTMLElement) => {
		const wheel = (e: WheelEvent) => zoomAt(e, node);
		node.addEventListener('wheel', wheel, { passive: false });
		return () => node.removeEventListener('wheel', wheel);
	};

	function zoomAt(e: WheelEvent, node: HTMLElement) {
		if (!width) return;
		e.preventDefault();
		const [a, c] = view ?? full;
		const box = node.getBoundingClientRect();
		const at = full[0] + ((e.clientX - box.left) / box.width) * span;
		const k = e.deltaY > 0 ? 1.25 : 0.8;
		set(at - (at - a) * k, at + (c - at) * k);
	}
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
	class="nav"
	role="group"
	aria-roledescription="range navigator"
	aria-label="{label}: {view ? `${fmt(view[0])} to ${fmt(view[1])}` : 'everything'}. Arrow keys move the window, plus and minus zoom, Escape shows everything."
	tabindex="0"
	bind:clientWidth={width}
	onkeydown={keydown}
	{@attach wheelZoom}
>
	{#if width > 0}
		<div class="plot" aria-hidden="true">
			<ChartCore
				{width}
				height={52}
				xDomain={full}
				yDomain={y}
				bind:context={context as never}
				brush={{ axis: 'x', onChange: onchange, classes: { range: 'window', handle: 'handle' } }}
			>
				<Svg>
					<Area data={rows} x="x" y0={() => y[0]} y1="y" {defined} class="area" />
					<Spline data={rows} x="x" y="y" {defined} class="line" />
				</Svg>
			</ChartCore>
		</div>
	{/if}
	<div class="labels">
		<span>{fmt(full[0])}</span>
		<span>{view ? `${fmt(view[0])} – ${fmt(view[1])}` : 'All'}</span>
		<span>{fmt(full[1])}</span>
	</div>
</div>

<style>
	.nav {
		display: grid;
		gap: var(--space-1);
		min-width: 0;
		border-radius: var(--radius-md);
	}
	.plot {
		overflow: hidden;
		background: var(--color-bg);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.plot :global(.area) {
		fill: var(--metric, var(--color-accent));
		fill-opacity: 0.2;
		stroke: none;
	}
	.plot :global(.line) {
		fill: none;
		stroke: var(--metric, var(--color-accent));
		stroke-opacity: 0.7;
		stroke-width: 1.25;
	}
	.plot :global(.window) {
		background: color-mix(in srgb, var(--metric, var(--color-accent)) 10%, transparent);
		border: 1.5px solid var(--metric, var(--color-accent));
		border-radius: var(--radius-sm);
	}
	.plot :global(.handle) {
		width: 8px;
	}
	.labels {
		display: flex;
		justify-content: space-between;
		gap: var(--space-2);
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--chart-axis);
	}
</style>
