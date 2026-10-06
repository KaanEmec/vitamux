<!--
	An ECG waveform on paper at 25 mm/s and 10 mm/mV (lib/watch/ecg.ts lays it out): a plain SVG
	grid, every millimetre and darker every 5 mm (0.2 s and 0.5 mV), with the trace in plain ink and
	a label each second. The strip is wider than the page, so it scrolls sideways in a focusable
	region (arrow keys scroll it). Below it, the per-second lows and highs as a table. Nothing is
	marked, rated or coloured by result.
-->
<script lang="ts">
	import { formatNumber } from './scale.ts';
	import ChartTable from './ChartTable.svelte';
	import { pxPerMM, pxPerMillivolt, pxPerSecond, type Strip } from '../watch/ecg.ts';

	let { strip, label }: { strip: Strip; label: string } = $props();

	const id = $props.id();
	const seconds = $derived(Array.from({ length: Math.ceil(strip.duration) }, (_, i) => i));
	/** The 0 mV line, a plain reference on the paper. */
	const zero = $derived(strip.high * pxPerMillivolt);
	const table = () => ({
		columns: ['Second', 'Lowest (mV)', 'Highest (mV)'],
		rows: strip.seconds.map((s) => [`${s.second}–${s.second + 1} s`, formatNumber(s.low, 2), formatNumber(s.high, 2)])
	});
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be focusable) -->
<div class="scroll" role="region" aria-label="{label}, scrolls sideways" tabindex="0">
	<svg width={strip.width} height={strip.height + 16} viewBox="0 -16 {strip.width} {strip.height + 16}" role="img" aria-label="{label}: {strip.summary}">
		<defs>
			<pattern id="{id}-minor" width={pxPerMM} height={pxPerMM} patternUnits="userSpaceOnUse">
				<path class="minor" d="M{pxPerMM} 0V{pxPerMM}H0" />
			</pattern>
			<pattern id="{id}-major" width={pxPerMM * 5} height={pxPerMM * 5} patternUnits="userSpaceOnUse">
				<rect width={pxPerMM * 5} height={pxPerMM * 5} fill="url(#{id}-minor)" />
				<path class="major" d="M{pxPerMM * 5} 0V{pxPerMM * 5}H0" />
			</pattern>
		</defs>
		<rect class="paper" width={strip.width} height={strip.height} fill="url(#{id}-major)" />
		<line class="zero" x1="0" x2={strip.width} y1={zero} y2={zero} />
		{#each seconds as s (s)}
			<text class="tick" x={s * pxPerSecond + 2} y="-4">{s} s</text>
		{/each}
		<path class="trace" d={strip.path} />
	</svg>
</div>
<ChartTable caption="{label}, lowest and highest value per second" data={table} />

<style>
	.scroll {
		overflow-x: auto;
		overflow-y: hidden;
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
		background: var(--color-surface);
	}
	svg {
		display: block;
	}
	.paper {
		stroke: none;
	}
	.minor,
	.major {
		fill: none;
		stroke: var(--metric-heart);
	}
	.minor {
		stroke-opacity: 0.18;
		stroke-width: 0.5;
	}
	.major {
		stroke-opacity: 0.45;
		stroke-width: 0.8;
	}
	.zero {
		stroke: var(--metric-heart);
		stroke-opacity: 0.45;
		stroke-dasharray: 2 3;
	}
	.tick {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		fill: var(--chart-axis);
	}
	.trace {
		fill: none;
		stroke: var(--color-text);
		stroke-width: 1.2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
</style>
