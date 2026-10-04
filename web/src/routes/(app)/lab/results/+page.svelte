<!--
	Confirmed lab results grouped by analyte, newest first, with the label, value, unit, range
	and flag exactly as printed. Unknown analytes are grouped by their printed label. Each known
	analyte links to its trend; a result's history lists its revisions.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { listResults, type LabResult } from '#lib/lab/api.ts';
	import HistoryDialog from '#lib/lab/HistoryDialog.svelte';
	import { printedValue } from '#lib/lab/format.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';

	let results = $state<LabResult[] | null>(null);
	let problem = $state<Problem | null>(null);
	let filter = $state('');
	let history = $state<LabResult | null>(null);

	onMount(async () => {
		const res = await listResults();
		problem = res.problem;
		results = res.data;
	});

	const groups = $derived.by(() => {
		const q = filter.trim().toLowerCase();
		const by: Record<string, { title: string; code: string | null; items: LabResult[] }> = {};
		for (const r of results ?? []) {
			if (q && !(r.analyte ?? '').includes(q) && !r.original_label.toLowerCase().includes(q)) continue;
			const key = r.analyte ?? `label:${r.original_label}`;
			by[key] ??= { title: r.analyte ?? r.original_label, code: r.analyte, items: [] };
			by[key].items.push(r);
		}
		for (const g of Object.values(by)) g.items.sort((a, b) => b.collected_date.localeCompare(a.collected_date));
		return Object.entries(by).sort(([, a], [, b]) => a.title.localeCompare(b.title));
	});
</script>

<svelte:head><title>Results · Lab results · Vitamux</title></svelte:head>

<section aria-labelledby="results">
	<h2 id="results">Confirmed results</h2>
	<p class="muted">Values, units, ranges and flags as the lab printed them. Converted values appear only where a conversion for the printed unit is listed.</p>
	<ProblemAlert {problem} />

	{#if results === null && !problem}
		<p class="muted" role="status">Loading results…</p>
	{:else if results && results.length === 0}
		<EmptyState title="No confirmed results yet." text="Upload and review a lab report under Documents." icon={icons.lab}>
			<a class="btn" href="/lab">Go to Documents</a>
		</EmptyState>
	{:else if results}
		<div class="field filter">
			<label for="filter">Filter by analyte or label</label>
			<input id="filter" type="search" bind:value={filter} autocomplete="off" />
		</div>
		<div class="groups">
			{#each groups as [key, g] (key)}
				{@const latest = g.items[0]}
				<section class="card group" aria-labelledby="g-{key}">
					<header>
						<div>
							<h3 id="g-{key}">{#if g.code}<code>{g.title}</code>{:else}{g.title} <span class="muted">(unknown analyte)</span>{/if}</h3>
							<p class="muted">
								{g.items.length} result{g.items.length === 1 ? '' : 's'} · latest {printedValue(latest)} {latest.unit_text ?? ''} on {latest.collected_date}
							</p>
						</div>
						{#if g.code}<a class="btn sm" href="/lab/analytes/{encodeURIComponent(g.code)}" aria-label="Trend of {g.title}">View trend</a>{/if}
					</header>
					<div class="wrap">
						<table>
							<caption class="visually-hidden">Results for {g.title}</caption>
							<thead>
								<tr>
									<th scope="col">Collected</th><th scope="col">Label as printed</th><th scope="col">Value</th>
									<th scope="col">Unit as printed</th><th scope="col">Range as printed</th><th scope="col">Flag as printed</th>
									<th scope="col">Converted</th><th scope="col">Laboratory</th><th scope="col"><span class="visually-hidden">History</span></th>
								</tr>
							</thead>
							<tbody>
								{#each g.items as r (r.id)}
									<tr>
										<th scope="row">{r.collected_date}</th>
										<td>{r.original_label}</td>
										<td>{printedValue(r)}</td>
										<td>{r.unit_text ?? 'unitless'}</td>
										<td>{r.reference_range_text ?? '–'}</td>
										<td>{r.printed_flag ?? '–'}</td>
										<td>{r.canonical_value !== null ? `${r.canonical_value} ${r.canonical_unit}` : '–'}</td>
										<td>{r.provenance.laboratory ?? '–'}</td>
										<td>
											<button class="btn link" type="button" onclick={() => (history = r)} aria-label="History of {r.original_label} on {r.collected_date}">
												History{r.revision > 1 ? ` (${r.revision} revisions)` : ''}
											</button>
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
				</section>
			{:else}
				<p class="muted">No result matches the filter.</p>
			{/each}
		</div>
	{/if}
</section>

{#if history}
	<HistoryDialog result={history} onclose={() => (history = null)} />
{/if}

<style>
	.filter {
		max-width: 24rem;
	}
	.groups {
		display: grid;
		gap: var(--space-4);
	}
	.group header {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: flex-start;
		justify-content: space-between;
		margin-bottom: var(--space-3);
	}
	.group h3 {
		margin-bottom: var(--space-1);
		font-size: var(--text-lg);
	}
	.group p {
		margin: 0;
		font-size: var(--text-sm);
	}
	.wrap {
		overflow-x: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--color-text-muted);
	}
	tbody tr:last-child > * {
		border-bottom: 0;
	}
</style>
