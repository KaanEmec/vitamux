<!--
	The distribution of a metric's values in a range: counts per value bin (round edges), in the
	metric hue, with the mean as a dashed marker. Missing values are left out, never counted as zero.
-->
<script lang="ts">
	import ChartFrame from './ChartFrame.svelte';
	import { extent, formatNumber, ticks } from './scale.ts';
	import type { TableData, Tip } from './types.ts';

	let {
		values,
		label,
		unit = '',
		noun = 'days',
		bins = 12,
		mean: showMean = true,
		height = 200
	}: {
		values: (number | null)[];
		label: string;
		unit?: string;
		/** What one value is, plural ("days", "nights", "readings"). */
		noun?: string;
		/** About how many bins. */
		bins?: number;
		mean?: boolean;
		height?: number;
	} = $props();

	const ok = $derived(values.filter((v): v is number => v != null && Number.isFinite(v)));
	const edges = $derived.by(() => {
		const [lo, hi] = extent(ok);
		const t = ticks([lo, hi], bins);
		const w = t.length > 1 ? t[1] - t[0] : 1;
		const out = t[0] > lo ? [t[0] - w, ...t] : [...t];
		while (out[out.length - 1] <= hi) out.push(out[out.length - 1] + w);
		return out;
	});
	const counts = $derived.by(() => {
		const c = edges.slice(1).map(() => 0);
		const w = edges[1] - edges[0];
		for (const v of ok) c[Math.min(Math.floor((v - edges[0]) / w), c.length - 1)]++;
		return c;
	});
	const mids = $derived(counts.map((_, i) => (edges[i] + edges[i + 1]) / 2));
	const avg = $derived(ok.length ? ok.reduce((a, b) => a + b, 0) / ok.length : null);
	const range = (i: number) => `${formatNumber(edges[i])}–${formatNumber(edges[i + 1])}${unit ? ` ${unit}` : ''}`;

	const tip = (i: number): Tip => ({ title: range(i), lead: { value: formatNumber(counts[i]), unit: noun }, rows: [] });
	const table = (): TableData => ({ columns: ['Range', noun[0].toUpperCase() + noun.slice(1)], rows: counts.map((c, i) => [range(i), String(c)]) });
</script>

<ChartFrame
	{label}
	xs={mids}
	x={[edges[0], edges[edges.length - 1]]}
	y={[0, Math.max(1, ...counts) * 1.12]}
	time={false}
	{height}
	crosshair={false}
	{tip}
	{table}
>
	{#snippet marks(f)}
		{@const w = f.sx(edges[1]) - f.sx(edges[0])}
		{#each counts as c, i (i)}
			{#if c}
				<rect
					class={['bar', f.active >= 0 && f.active !== i && 'dim']}
					x={f.sx(edges[i]) + 1.5}
					y={f.sy(c)}
					width={Math.max(1, w - 3)}
					height={f.sy(0) - f.sy(c)}
					rx={Math.min(4, w / 4)}
				/>
			{/if}
		{/each}
		{#if showMean && avg != null}
			<line class="mean" x1={f.sx(avg)} x2={f.sx(avg)} y1={f.top} y2={f.bottom} />
			<text class="mean-label" x={f.sx(avg) + 6} y={f.top + 10}>mean {formatNumber(avg)}</text>
		{/if}
	{/snippet}
</ChartFrame>

<style>
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.bar {
		fill: var(--metric, var(--color-accent));
		fill-opacity: 0.85;
	}
	.bar.dim {
		fill-opacity: 0.5;
	}
	.mean {
		stroke: var(--color-text-muted);
		stroke-width: 1.5;
		stroke-dasharray: 4 4;
	}
	.mean-label {
		font-size: var(--text-2xs);
		fill: var(--color-text-muted);
	}
</style>
