<!--
	Daily view: one metric over a date range, one resolved value per local day with its
	status, rule and an explanation popover. Each day links to the all-sources drilldown.
	Query: ?metric=&start=&end= (shareable). Resolved endpoints that are not available
	yet (404/503) show their problem and leave the page usable.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ExplainPopover from '#lib/components/ExplainPopover.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ResultStatus from '#lib/components/ResultStatus.svelte';
	import { addDays, datesDescending, formatValue, isDate, metricLabel, today } from '#lib/data/format.ts';

	type Resolved = Schemas['ResolvedValue'];

	const maxDays = 92;
	// Shown when GET /metrics is unavailable; codes from docs/metrics.md.
	const commonMetrics = ['steps', 'resting_heart_rate', 'heart_rate', 'weight', 'blood_pressure', 'spo2'];

	let metrics = $state<string[]>([]);
	let metricsProblem = $state<Problem | null>(null);
	let problem = $state<Problem | null>(null);
	let loading = $state(false);
	let timezone = $state('');
	let byDate = $state<Record<string, Resolved | undefined>>({});

	const end = $derived.by(() => {
		const e = page.url.searchParams.get('end');
		return isDate(e) ? e : today();
	});
	const start = $derived.by(() => {
		const s = page.url.searchParams.get('start');
		return isDate(s) && s <= end ? s : addDays(end, -13);
	});
	const options = $derived(metrics.length ? metrics : commonMetrics);
	const metric = $derived(page.url.searchParams.get('metric') || (options.includes('steps') ? 'steps' : options[0]));
	const dates = $derived(datesDescending(start, end));

	let formMetric = $state('');
	let formStart = $state('');
	let formEnd = $state('');
	let formError = $state('');
	$effect(() => {
		formMetric = metric;
		formStart = start;
		formEnd = end;
	});

	onMount(async () => {
		const res = await api.GET('/api/v1/metrics');
		if (res.error) {
			metricsProblem = res.error;
			return;
		}
		const list = res.data.metrics as unknown as { code?: string; windows?: string[] }[];
		metrics = list
			.filter((m) => m.code && (!m.windows || m.windows.includes('local_day')))
			.map((m) => m.code as string)
			.sort();
	});

	let generation = 0;
	$effect(() => {
		const [m, s, e] = [metric, start, end];
		if (!m) return;
		const mine = ++generation;
		loading = true;
		api
			.GET('/api/v1/resolved/daily', { params: { query: { start_date: s, end_date: e, metrics: [m] } } })
			.then((res) => {
				if (mine !== generation) return;
				loading = false;
				problem = res.error ?? null;
				byDate = {};
				if (!res.data) return;
				timezone = res.data.timezone;
				for (const d of res.data.days) byDate[d.local_date] = d.metrics[m];
			});
	});

	function submit(e: SubmitEvent) {
		e.preventDefault();
		formError = '';
		if (formStart > formEnd) {
			formError = 'The first date must not be after the last date.';
			return;
		}
		if (datesDescending(formStart, formEnd).length > maxDays) {
			formError = `Choose at most ${maxDays} days.`;
			return;
		}
		const q = new URLSearchParams({ metric: formMetric, start: formStart, end: formEnd });
		void goto(`/data?${q}`);
	}

	const warningCodes = (r: Resolved) => (r.warnings ?? []).map((w) => (w.group ? `${w.code} (${w.group})` : w.code));
</script>

<svelte:head><title>Data · Vitamux</title></svelte:head>

<h2>Daily values</h2>
<p class="muted">
	One value per local day, chosen by the metric's rule. Open a day to see every source and why one was used.
</p>

<form class="controls" onsubmit={submit}>
	<div class="field">
		<label for="metric">Metric</label>
		<select id="metric" bind:value={formMetric}>
			{#each options as m (m)}<option value={m}>{metricLabel(m)}</option>{/each}
		</select>
	</div>
	<div class="field">
		<label for="start">From</label>
		<input id="start" type="date" bind:value={formStart} required />
	</div>
	<div class="field">
		<label for="end">To</label>
		<input id="end" type="date" bind:value={formEnd} required />
	</div>
	<div class="field"><button class="btn primary" type="submit">Show</button></div>
</form>
{#if formError}<p class="form-error" role="alert">{formError}</p>{/if}
{#if metricsProblem}
	<p class="muted">The metric list is unavailable ({metricsProblem.detail}); showing common metrics.</p>
{/if}

<ProblemAlert {problem} />

<div class="card table-wrap" aria-busy={loading}>
	<table>
		<caption class="visually-hidden">
			Resolved {metricLabel(metric)} per local day{timezone ? `, ${timezone}` : ''}
		</caption>
		<thead>
			<tr><th scope="col">Date</th><th scope="col">Status</th><th scope="col">Value</th><th scope="col">Rule</th><th scope="col">Why</th><th scope="col">Sources</th></tr>
		</thead>
		<tbody>
			{#each dates as date (date)}
				{@const r = byDate[date]}
				<tr>
					<th scope="row">{date}</th>
					<td><ResultStatus status={r?.status ?? 'no_data'} /></td>
					<td class="num">
						{#if r && r.status !== 'no_data'}
							{formatValue(r.value, r.unit)}{#if r.partial}<span class="muted"> (partial)</span>{/if}
						{:else}–{/if}
					</td>
					<td class="muted">{r?.rule ? `${r.rule.ref} v${r.rule.version}` : '–'}</td>
					<td>
						{#if r}<ExplainPopover text={r.explanation} warnings={warningCodes(r)} label="Explain" />{/if}
					</td>
					<td><a href="/data/day/{encodeURIComponent(metric)}/{date}">All sources</a></td>
				</tr>
			{/each}
		</tbody>
	</table>
	{#if !loading && !problem && Object.keys(byDate).length === 0}
		<p class="muted">No resolved values in this range.</p>
	{/if}
</div>

<style>
	.controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: end;
	}
	.controls .field {
		margin-bottom: var(--space-2);
	}
	select,
	input[type='date'] {
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.form-error {
		color: var(--color-error);
	}
	.table-wrap {
		padding: var(--space-3);
	}
	table {
		width: 100%;
		border-collapse: collapse;
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
		vertical-align: middle;
	}
	thead th {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	tbody th {
		font-weight: 600;
		white-space: nowrap;
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
</style>
