<!--
	A workout route as a plain SVG path on a local plane (lib/watch/watch.ts routePoints): no map,
	no tiles and no third-party request, so locations never leave the server. North is up and both
	axes share one scale; a 1 km bar, a start circle and an end square (shape, not colour, tells
	them apart). The points are also available as a table.
-->
<script lang="ts">
	import ChartTable from './ChartTable.svelte';
	import { formatInstant, formatNumber } from './scale.ts';
	import type { RoutePoint } from '../watch/watch.ts';

	let { points, count, label, timezone }: { points: RoutePoint[]; count: number; label: string; timezone?: string } = $props();

	const size = 320;
	const pad = 16;
	const box = $derived.by(() => {
		const xs = points.map((p) => p.x);
		const ys = points.map((p) => p.y);
		const [x0, x1, y0, y1] = [Math.min(...xs), Math.max(...xs), Math.min(...ys), Math.max(...ys)];
		const span = Math.max(x1 - x0, y1 - y0, 1);
		const k = (size - 2 * pad) / span;
		return { k, x: (v: number) => pad + (v - x0) * k + ((size - 2 * pad) - (x1 - x0) * k) / 2, y: (v: number) => pad + (v - y0) * k + ((size - 2 * pad) - (y1 - y0) * k) / 2 };
	});
	const d = $derived(points.map((p, i) => `${i ? 'L' : 'M'}${box.x(p.x).toFixed(1)},${box.y(p.y).toFixed(1)}`).join(''));
	/** A scale bar of a round length that fits a third of the plot. */
	const bar = $derived.by(() => {
		const metres = (size - 2 * pad) / 3 / box.k;
		const step = [10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000].findLast((s) => s <= metres) ?? 10;
		return { px: step * box.k, label: step >= 1000 ? `${step / 1000} km` : `${step} m` };
	});
	const first = $derived(points[0]);
	const last = $derived(points[points.length - 1]);
	const table = () => ({
		columns: ['Time', 'Latitude', 'Longitude', 'Altitude (m)', 'Speed (m/s)'],
		rows: points.map((p) => [formatInstant(p.t, timezone), p.lat.toFixed(5), p.lon.toFixed(5), p.alt == null ? '–' : formatNumber(p.alt), p.speed == null ? '–' : formatNumber(p.speed, 2)])
	});
</script>

<figure>
	<svg viewBox="0 0 {size} {size}" role="img" aria-label="{label}: {count.toLocaleString()} locations as recorded, drawn without a map, north up">
		<path class="route" {d} />
		<circle class="mark" cx={box.x(first.x)} cy={box.y(first.y)} r="5" />
		<rect class="mark" x={box.x(last.x) - 4.5} y={box.y(last.y) - 4.5} width="9" height="9" />
		<g class="scale" transform="translate({pad} {size - pad / 2})">
			<line x1="0" x2={bar.px} y1="0" y2="0" />
			<text x={bar.px + 4} y="3">{bar.label}</text>
		</g>
	</svg>
	<figcaption class="legend">
		<span><svg width="12" height="12" aria-hidden="true"><circle class="mark" cx="6" cy="6" r="5" /></svg>Start</span>
		<span><svg width="12" height="12" aria-hidden="true"><rect class="mark" x="1.5" y="1.5" width="9" height="9" /></svg>End</span>
		<span>North is up · no map tiles</span>
	</figcaption>
</figure>
<ChartTable caption="{label}, locations" data={table} />

<style>
	figure {
		margin: 0;
	}
	svg[role='img'] {
		display: block;
		width: 100%;
		max-width: 28rem;
		height: auto;
		margin: 0 auto;
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
		background: var(--color-surface);
	}
	.route {
		fill: none;
		stroke: var(--metric-activity);
		stroke-width: 3;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.mark {
		fill: var(--color-surface);
		stroke: var(--color-text);
		stroke-width: 2;
	}
	.scale line {
		stroke: var(--chart-axis);
		stroke-width: 2;
	}
	.scale text {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		fill: var(--chart-axis);
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		justify-content: center;
		gap: var(--space-2) var(--space-4);
		margin-top: var(--space-2);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.legend span {
		display: inline-flex;
		align-items: center;
		gap: var(--space-1);
	}
</style>
