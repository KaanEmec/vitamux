<!-- The metric page's stats header: latest value, period mean, range and coverage, each with a neutral sub-line. -->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import { formatNumber } from '../charts/scale.ts';
	import { dayLabel } from '../views/format.ts';
	import { num } from './series.ts';

	let {
		metric,
		summary,
		trend,
		values,
		days,
		span,
		unit,
		mean,
		previousMean
	}: {
		metric: string;
		summary: Schemas['MetricSummary'] | null;
		/** Rollups ("All"), or null for one value per day. */
		trend: Schemas['ResolvedTrend'] | null;
		/** The plotted values. */
		values: number[];
		/** Days in the range. */
		days: number;
		/** Days in the range, or null for All. */
		span: number | null;
		unit: string;
		mean: number | null;
		/** The mean of the period before the range. */
		previousMean?: number | null;
	} = $props();

	const signed = (v: number) => `${v > 0 ? '+' : v < 0 ? '−' : '±'}${formatNumber(Math.abs(v))}`;
	const bounds = (pick: (b: Schemas['Rollup']) => number | null | undefined) => (trend ? trend.buckets.flatMap((b) => pick(b) ?? []) : values);
	const lowest = $derived(Math.min(...bounds((b) => b.min)));
	const highest = $derived(Math.max(...bounds((b) => b.max)));
	const withData = $derived(
		trend ? [trend.buckets.reduce((n, b) => n + b.n, 0), trend.buckets.reduce((n, b) => n + b.days, 0)] : [values.length, days]
	);
	const thirty = $derived(summary?.stats[1]?.mean);
	const latest = $derived(summary?.sparkline.findLast((p) => num(p.value, metric) != null));
	const latestValue = $derived(latest ? num(latest.value, metric) : null);

	const stats = $derived([
		{
			label: 'Latest',
			value: latestValue == null ? '–' : formatNumber(latestValue),
			unit,
			sub: latest ? `${dayLabel(latest.local_date)}${thirty != null && latestValue != null ? ` · ${signed(latestValue - thirty)} vs 30-day mean` : ''}` : 'Nothing in the last 30 days'
		},
		{
			label: span ? `${span}-day mean` : 'Mean',
			value: mean == null ? '–' : formatNumber(mean),
			unit,
			sub: !span ? 'All stored values' : mean != null && previousMean != null ? `${signed(mean - previousMean)} vs previous ${span} days` : 'No previous period'
		},
		{ label: 'Range', value: Number.isFinite(lowest) ? `${formatNumber(lowest)}–${formatNumber(highest)}` : '–', unit: '', sub: `min – max${unit ? `, ${unit}` : ''}` },
		{ label: 'Coverage', value: withData[0].toLocaleString(), unit: `/ ${withData[1].toLocaleString()}`, sub: 'days with a value' }
	]);
</script>

<ul class="stats" aria-label="Statistics">
	{#each stats as s (s.label)}
		<li>
			<span class="lbl">{s.label}</span>
			<span class="stat"><strong>{s.value}</strong>{#if s.unit}<span class="unit">{s.unit}</span>{/if}</span>
			<span class="muted small">{s.sub}</span>
		</li>
	{/each}
</ul>

<style>
	.stats {
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: grid;
		align-content: start;
		gap: var(--space-1);
		min-width: 0;
	}
	.lbl {
		font-size: var(--text-2xs);
		font-weight: 500;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--color-text-muted);
	}
	.stat {
		display: flex;
		align-items: baseline;
		gap: var(--space-1);
		font-variant-numeric: tabular-nums;
	}
	strong {
		font-size: var(--text-2xl);
		font-weight: 600;
		line-height: 1.1;
		letter-spacing: var(--tracking-tight);
	}
	.unit {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.small {
		font-size: var(--text-xs);
	}
	@media (max-width: 40rem) {
		.stats {
			grid-template-columns: repeat(2, minmax(0, 1fr));
			row-gap: var(--space-4);
		}
	}
</style>
