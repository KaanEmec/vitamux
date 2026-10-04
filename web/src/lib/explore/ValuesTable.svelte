<!-- The metric page's values, newest first: each day with its status, source and rule (Explain, Details, All sources), or each rollup's mean, min and max. -->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import { formatNumber } from '../charts/scale.ts';
	import ExplainPopover from '../components/ExplainPopover.svelte';
	import ResultStatus from '../components/ResultStatus.svelte';
	import { providerLabel } from '../connections/connections.ts';
	import { formatValue } from '../data/format.ts';
	import { dayLabel } from '../views/format.ts';
	import { dayProviders, warningCodes } from './series.ts';

	type Resolved = Schemas['ResolvedValue'];

	let {
		metric,
		unit,
		trend,
		dates,
		daily,
		selected,
		onselect
	}: {
		metric: string;
		unit: string;
		/** Rollups ("All"); otherwise one row per date. */
		trend: Schemas['ResolvedTrend'] | null;
		dates: string[];
		daily: Record<string, Resolved | undefined> | null;
		selected: string | null;
		onselect: (date: string) => void;
	} = $props();

	const pageRows = 31;
	// Back to one page whenever the values load again.
	let rows = $derived((void [trend, daily], pageRows));
	const total = $derived(trend?.buckets.length ?? dates.length);

	const fmt = (v: number | null | undefined) => (v == null ? '–' : `${formatNumber(v)}${unit ? ` ${unit}` : ''}`);
	const ruleName = (r: Schemas['RuleRef'] | undefined) => (!r ? '–' : r.ref.startsWith('builtin:') ? 'Built-in' : `v${r.version}`);
	const sourceText = (r: Resolved | undefined) => dayProviders(r).map(providerLabel).join(', ') || '–';
</script>

<section class="card values" aria-labelledby="values-h">
	<div class="card-head">
		<h2 id="values-h">Values</h2>
		<span class="muted small">Newest first</span>
	</div>
	<div class="scroll">
		{#if trend}
			<table>
				<thead>
					<tr><th scope="col">{trend.grain === 'week' ? 'Week of' : 'Month of'}</th><th scope="col" class="num">Mean</th><th scope="col" class="num">Min</th><th scope="col" class="num">Max</th><th scope="col" class="num">Days with data</th></tr>
				</thead>
				<tbody>
					{#each trend.buckets.toReversed().slice(0, rows) as b (b.start_date)}
						<tr>
							<th scope="row">{dayLabel(b.start_date)}</th>
							<td class="num">{fmt(b.mean)}</td>
							<td class="num">{fmt(b.min)}</td>
							<td class="num">{fmt(b.max)}</td>
							<td class="num">{b.n} / {b.days}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{:else}
			<table>
				<thead>
					<tr><th scope="col">Date</th><th scope="col" class="num">Value</th><th scope="col">Status</th><th scope="col" class="wide">Source</th><th scope="col" class="wide">Rule</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr>
				</thead>
				<tbody>
					{#each dates.toReversed().slice(0, rows) as d (d)}
						{@const r = daily?.[d]}
						<tr class:current={selected === d}>
							<th scope="row">{dayLabel(d)}</th>
							<td class="num">
								{#if r && r.status !== 'no_data'}{formatValue(r.value, r.unit)}{:else}–{/if}
							</td>
							<td><ResultStatus status={r?.status ?? 'no_data'} partial={r?.partial} /></td>
							<td class="wide">{sourceText(r)}</td>
							<td class="wide muted">{ruleName(r?.rule)}</td>
							<td class="row-actions">
								{#if r}<ExplainPopover text={r.explanation} warnings={warningCodes(r)} label="Explain" />{/if}
								<button class="btn link" type="button" onclick={() => onselect(d)}>Details<span class="visually-hidden"> of {d}</span></button>
								<a href="/explore/{encodeURIComponent(metric)}/day/{d}">All sources</a>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		{/if}
	</div>
	{#if total > rows}
		<button class="btn ghost sm more" type="button" onclick={() => (rows = Infinity)}>Show all {total}</button>
	{/if}
</section>

<style>
	.values {
		flex: 2 1 34rem;
		min-width: 0;
		padding: 0;
	}
	.card-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
		padding: var(--space-4) var(--space-4) var(--space-2);
	}
	h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	.small {
		font-size: var(--text-xs);
	}
	.scroll {
		overflow-x: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
		font-variant-numeric: tabular-nums;
	}
	th,
	td {
		padding: var(--space-2) var(--space-4);
		text-align: left;
		white-space: nowrap;
		border-top: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
	}
	tbody th {
		font-weight: 500;
	}
	.num {
		text-align: right;
	}
	tr.current {
		background: var(--color-selected);
	}
	.row-actions {
		display: flex;
		gap: var(--space-3);
		align-items: center;
	}
	.more {
		margin: var(--space-2) var(--space-4) var(--space-3);
	}
	@media (max-width: 40rem) {
		.wide {
			display: none;
		}
		th,
		td {
			padding: var(--space-2);
			white-space: normal;
		}
		.row-actions {
			flex-direction: column;
			align-items: flex-start;
			gap: var(--space-1);
		}
	}
</style>
