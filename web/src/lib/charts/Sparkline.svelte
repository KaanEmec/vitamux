<!--
	A small trend for cards: a line (or bars) with an optional range band and mean line, and an
	optional dashed `ghost` line over the same points (a draft beside the rule in effect). No axes or
	interaction; give `label` when it carries meaning, otherwise it is decorative.
-->
<script lang="ts">
	import { extent, linear, linePath } from './scale.ts';

	let {
		ys,
		bars = false,
		band,
		mean,
		ghost,
		label = ''
	}: {
		ys: (number | null)[];
		bars?: boolean;
		band?: [number, number];
		mean?: number;
		ghost?: (number | null)[];
		label?: string;
	} = $props();

	const w = 240;
	const h = 48;
	const xs = $derived(ys.map((_, i) => i));
	const y = $derived(extent([...ys, ...(ghost ?? []), ...(band ?? []), mean, ...(bars ? [0] : [])], bars ? 0 : 0.1));
	const sx = $derived(bars ? linear([0, ys.length], [0, w]) : linear([0, Math.max(ys.length - 1, 1)], [2, w - 2]));
	const sy = $derived(linear(y, [h - 3, 3]));
</script>

<svg
	class="spark"
	viewBox="0 0 {w} {h}"
	preserveAspectRatio="none"
	role={label ? 'img' : undefined}
	aria-label={label || undefined}
	aria-hidden={label ? undefined : 'true'}
>
	{#if band}<rect class="band" x="0" width={w} y={sy(band[1])} height={Math.max(0, sy(band[0]) - sy(band[1]))} />{/if}
	{#if bars}
		{#each ys as v, i (i)}
			{#if v}<rect class={['bar', i === ys.length - 1 && 'last']} x={sx(i) + 1.5} width={Math.max(1, sx(1) - sx(0) - 3)} y={sy(v)} height={sy(0) - sy(v)} rx="2" />{/if}
		{/each}
	{:else}
		<path class="line" d={linePath(xs, ys, sx, sy)} />
	{/if}
	{#if ghost}<path class="line ghost" d={linePath(xs, ghost, sx, sy)} />{/if}
	{#if mean != null}<line class="mean" x1="0" x2={w} y1={sy(mean)} y2={sy(mean)} />{/if}
</svg>

<style>
	/* Drawn in the metric hue when a parent sets --metric (lib/ui/metric.ts). */
	.spark {
		--hue: var(--metric, var(--color-accent));
		display: block;
		width: 100%;
		height: 3rem;
	}
	.band {
		fill: color-mix(in srgb, var(--hue) 12%, transparent);
	}
	.line {
		fill: none;
		stroke: var(--hue);
		stroke-width: 2;
		stroke-linejoin: round;
		vector-effect: non-scaling-stroke;
	}
	.ghost {
		stroke: var(--color-text);
		stroke-width: 1.5;
		stroke-dasharray: 4 3;
	}
	.bar {
		fill: color-mix(in srgb, var(--hue) 40%, transparent);
	}
	.bar.last {
		fill: var(--hue);
	}
	.mean {
		stroke: var(--color-text-muted);
		stroke-dasharray: 3 3;
		vector-effect: non-scaling-stroke;
	}
</style>
