<!--
	uPlot overlay of several sources over one day. `series` carries x in epoch seconds.
	Sources differ by colour AND dash pattern. `timezone` (IANA) sets the axis clock.
	Marks "vx-chart-render" in the User Timing log (data join to first paint) for the
	render-time budget (docs/plan/E11-frontend/J11.3-data-provenance.md).
-->
<script lang="ts" module>
	export interface ChartSeries {
		label: string;
		xs: number[];
		ys: number[];
	}
</script>

<script lang="ts">
	import uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';

	let {
		series,
		unit = '',
		timezone = undefined,
		summary = ''
	}: { series: ChartSeries[]; unit?: string; timezone?: string; summary?: string } = $props();

	let host: HTMLDivElement;
	const dashes: number[][] = [[], [8, 4], [2, 4], [10, 3, 2, 3], [4, 4], [1, 3]];

	function css(el: HTMLElement, name: string): string {
		return getComputedStyle(el).getPropertyValue(name).trim();
	}

	$effect(() => {
		if (!series.length) return;
		performance.mark('vx-chart-start');
		const data = uPlot.join(series.map((s) => [s.xs, s.ys] as uPlot.AlignedData));
		const text = css(host, '--color-text-muted');
		const grid = css(host, '--color-border');
		const tz = timezone ? (ts: number) => uPlot.tzDate(new Date(ts * 1000), timezone) : undefined;
		const opts: uPlot.Options = {
			width: Math.max(host.clientWidth, 320),
			height: 280,
			tzDate: tz,
			scales: { x: { time: true } },
			axes: [
				{ stroke: text, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid } },
				{ stroke: text, grid: { stroke: grid, width: 1 }, ticks: { stroke: grid }, label: unit, size: 64 }
			],
			series: [
				{},
				...series.map((s, i) => ({
					label: s.label,
					stroke: css(host, `--chart-${(i % 6) + 1}`),
					width: 2,
					dash: dashes[i % dashes.length],
					spanGaps: false,
					points: { size: 6 }
				}))
			],
			cursor: { drag: { x: true, y: false } }
		};
		const plot = new uPlot(opts, data, host);
		requestAnimationFrame(() => {
			performance.mark('vx-chart-end');
			performance.measure('vx-chart-render', 'vx-chart-start', 'vx-chart-end');
		});
		const ro = new ResizeObserver(() => plot.setSize({ width: Math.max(host.clientWidth, 320), height: 280 }));
		ro.observe(host);
		return () => {
			ro.disconnect();
			plot.destroy();
		};
	});
</script>

<div class="chart" bind:this={host} role="group" aria-label={summary || 'Overlay chart of every source'}></div>

<style>
	.chart {
		--chart-1: #0f766e;
		--chart-2: #b45309;
		--chart-3: #1d4ed8;
		--chart-4: #a21caf;
		--chart-5: #be123c;
		--chart-6: #4d7c0f;
		width: 100%;
		min-height: 280px;
	}
	@media (prefers-color-scheme: dark) {
		.chart {
			--chart-1: #2dd4bf;
			--chart-2: #fbbf24;
			--chart-3: #93c5fd;
			--chart-4: #e879f9;
			--chart-5: #fb7185;
			--chart-6: #a3e635;
		}
	}
	.chart :global(.u-legend) {
		color: var(--color-text);
		font-size: var(--text-sm);
	}
</style>
