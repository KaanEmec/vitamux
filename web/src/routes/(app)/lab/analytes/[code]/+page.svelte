<!--
	One analyte over time (J21.9): the confirmed results as printed, plotted in one printed unit
	with the reference range printed on each report. Values, units, ranges and flags are shown as
	the lab printed them; nothing is flagged or rated here. The route param is the analyte code,
	or `label:<printed label>` for a result whose analyte is unknown.
-->
<script lang="ts">
	import { page } from '$app/state';
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { listResults, type LabResult } from '#lib/lab/api.ts';
	import { printedValue } from '#lib/lab/format.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Segmented from '#lib/ui/Segmented.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import Stats from '#lib/views/Stats.svelte';
	import TableCard from '#lib/views/TableCard.svelte';

	const pageSize = 50;

	let all = $state<LabResult[] | null>(null);
	let problem = $state<Problem | null>(null);
	let unit = $state<string | null>(null);
	let shown = $state(pageSize);

	const code = $derived(page.params.code ?? '');

	$effect(() => {
		let stale = false;
		void listResults().then((res) => {
			if (stale) return;
			problem = res.problem;
			all = res.data;
		});
		return () => (stale = true);
	});

	// Newest first. An unknown analyte is addressed by its printed label.
	const results = $derived(
		(all ?? [])
			.filter((r) => (code.startsWith('label:') ? !r.analyte && r.original_label === code.slice(6) : r.analyte === code))
			.sort((a, b) => b.collected_date.localeCompare(a.collected_date))
	);
	const latest = $derived(results[0]);
	const units = $derived([...new Set(results.map((r) => r.unit_text ?? ''))]);
	const shownUnit = $derived(unit !== null && units.includes(unit) ? unit : (latest?.unit_text ?? ''));
	const unitLabel = (u: string) => u || 'unitless';

	// Plotted: the numeric results printed in one unit, oldest first, at noon UTC of the collection date.
	const plotted = $derived(results.filter((r) => r.value_numeric !== null && (r.unit_text ?? '') === shownUnit).reverse());
	const xs = $derived(plotted.map((r) => Date.parse(`${r.collected_date}T12:00:00Z`)));
	const ranged = $derived(plotted.some((r) => r.ref_low !== null && r.ref_high !== null));

	const stats = $derived(
		latest
			? [
					{ k: `Latest result · ${latest.collected_date}`, v: printedValue(latest), u: latest.unit_text ?? undefined },
					{ k: 'Results', v: String(results.length) }
				]
			: []
	);
</script>

<svelte:head><title>{latest?.original_label ?? 'Analyte'} · Lab results · Vitamux</title></svelte:head>

<p><a href="/lab/results">All results</a></p>

<ProblemAlert {problem} />

{#if all === null && !problem}
	<Skeleton variant="chart" label="Loading results" />
{:else if !latest}
	{#if !problem}<EmptyState icon={icons.lab} title="No confirmed results for this analyte" text="Confirm a lab report that includes it, and its results appear here." />{/if}
{:else}
	<h2>{latest.original_label} {#if latest.analyte}<code class="muted">{latest.analyte}</code>{/if}</h2>
	<Stats items={stats} />

	<section class="card" aria-labelledby="trend-h">
		<div class="head">
			<h3 id="trend-h">Over time</h3>
			{#if units.length > 1}
				<Segmented label="Printed unit" options={units.map((u) => ({ value: u, label: unitLabel(u) }))} value={shownUnit} onchange={(u) => (unit = u)} />
			{/if}
		</div>
		<p class="muted note">
			Plotted in {unitLabel(shownUnit)}, as printed. {#if units.length > 1}Results printed in another unit are in the table.{/if}
			{#if ranged}The band is the reference range printed on each report.{/if}
		</p>
		{#if plotted.length}
			{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
				<TimeSeries
					series={[{ label: latest.original_label, xs, ys: plotted.map((r) => r.value_numeric), style: 'dots' }]}
					band={ranged ? { xs, lo: plotted.map((r) => r.ref_low), hi: plotted.map((r) => r.ref_high), label: 'Reference range as printed' } : undefined}
					label="{latest.original_label} results over time"
					unit={shownUnit}
					withTime={false}
				/>
			{/await}
		{:else}
			<p class="muted">No numeric results in this unit.</p>
		{/if}
	</section>

	<TableCard title="Results as printed" remaining={results.length - shown} onmore={() => (shown += pageSize)}>
		<thead>
			<tr>
				<th scope="col">Collected</th><th scope="col">Label as printed</th><th scope="col" class="num">Value</th><th scope="col">Unit as printed</th>
				<th scope="col">Range as printed</th><th scope="col">Flag as printed</th><th scope="col">Laboratory</th><th scope="col">Document</th>
			</tr>
		</thead>
		<tbody>
			{#each results.slice(0, shown) as r (r.id)}
				<tr>
					<th scope="row">{r.collected_date}</th>
					<td>{r.original_label}</td>
					<td class="num">{printedValue(r)}</td>
					<td>{r.unit_text ?? 'unitless'}</td>
					<td>{r.reference_range_text ?? '–'}</td>
					<td>{r.printed_flag ?? '–'}</td>
					<td>{r.provenance.laboratory ?? '–'}</td>
					<td><a href="/lab/documents/{r.provenance.document_id}">Open<span class="visually-hidden"> the document of {r.collected_date}</span></a></td>
				</tr>
			{/each}
		</tbody>
	</TableCard>
{/if}

<style>
	h2 {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-3);
	}
	.card {
		margin-bottom: var(--space-4);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
	}
	h3 {
		margin: 0;
	}
	.note {
		margin: var(--space-1) 0 var(--space-3);
		font-size: var(--text-sm);
	}
</style>
