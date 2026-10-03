<!--
	Workouts: overlapping workouts from different sources grouped into clusters (the same
	rule the resolver uses), each member with its own numbers and provenance. Query: ?start=&end=.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { clusterWorkouts, type Cluster, type Workout } from '#lib/data/cluster.ts';
	import { addDays, clock, datesDescending, duration, formatValue, isDate, metricLabel, today } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';

	const maxDays = 92;

	let clusters = $state<Cluster[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(false);
	let provenanceId = $state<string | null>(null);

	const end = $derived.by(() => {
		const e = page.url.searchParams.get('end');
		return isDate(e) ? e : today();
	});
	const start = $derived.by(() => {
		const s = page.url.searchParams.get('start');
		return isDate(s) && s <= end ? s : addDays(end, -13);
	});
	let formStart = $state('');
	let formEnd = $state('');
	let formError = $state('');
	$effect(() => {
		formStart = start;
		formEnd = end;
	});

	let generation = 0;
	$effect(() => {
		const [s, e] = [start, end];
		const mine = ++generation;
		loading = true;
		readAll<Workout>(async (cursor) => {
			const res = await api.GET('/api/v1/workouts', {
				params: { query: { start_date: s, end_date: e, limit: 500, cursor } }
			});
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.workouts, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((r) => {
			if (mine !== generation) return;
			loading = false;
			problem = r.problem ?? null;
			clusters = clusterWorkouts(r.items).reverse();
		});
	});

	function submit(e: SubmitEvent) {
		e.preventDefault();
		formError = '';
		if (formStart > formEnd) formError = 'The first date must not be after the last date.';
		else if (datesDescending(formStart, formEnd).length > maxDays) formError = `Choose at most ${maxDays} days.`;
		else void goto(`/data/workouts?start=${formStart}&end=${formEnd}`);
	}

	function source(w: Workout): string {
		const detail = w.source.origin ?? w.source.device_type;
		return detail ? `${w.source.provider} · ${detail}` : w.source.provider;
	}
	const span = (c: Cluster) => {
		const first = c.workouts[0];
		return `${first.local_date} ${clock(new Date(c.start).toISOString(), first.tz_offset_min)}–${clock(new Date(c.end).toISOString(), first.tz_offset_min)}`;
	};
	const seconds = (w: Workout) => (Date.parse(w.end_at) - Date.parse(w.start_at)) / 1000;
	const km = (m: number | null) => (m == null ? '–' : formatValue(m / 1000, 'km'));
</script>

<svelte:head><title>Workouts · Vitamux</title></svelte:head>

<h2>Workouts</h2>
<p class="muted">
	Workouts that overlap in time are listed together, one row per source, so duplicates across sources are visible.
</p>

<form class="controls" onsubmit={submit}>
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

<ProblemAlert {problem} />

{#if loading}
	<p class="muted">Loading workouts…</p>
{:else if !clusters.length && !problem}
	<p class="muted">No workouts in this range.</p>
{/if}

<ul class="clusters">
	{#each clusters as c (c.workouts[0].id)}
		<li class="card">
			<h3>
				{metricLabel(c.sport)}
				<span class="muted">· {span(c)}</span>
				{#if c.workouts.length > 1}<span class="badge">{c.workouts.length} sources</span>{/if}
			</h3>
			<table>
				<thead>
					<tr>
						<th scope="col">Source</th><th scope="col">Sport as reported</th><th scope="col">Duration</th>
						<th scope="col">Distance</th><th scope="col">Energy</th><th scope="col">Avg / max HR</th><th scope="col">Provenance</th>
					</tr>
				</thead>
				<tbody>
					{#each c.workouts as w (w.id)}
						<tr>
							<th scope="row">{source(w)}</th>
							<td>{w.provider_sport ?? w.sport}</td>
							<td>{duration(seconds(w))}</td>
							<td>{km(w.distance_m)}</td>
							<td>{w.energy_kcal == null ? '–' : formatValue(w.energy_kcal, 'kcal')}</td>
							<td>{w.avg_hr_bpm == null ? '–' : formatValue(w.avg_hr_bpm)} / {w.max_hr_bpm == null ? '–' : formatValue(w.max_hr_bpm)} bpm</td>
							<td>
								<button class="btn link" type="button" onclick={() => (provenanceId = w.id)}>
									Provenance<span class="visually-hidden"> of {source(w)} workout</span>
								</button>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</li>
	{/each}
</ul>

{#if provenanceId}
	<ProvenanceDialog entity="workout" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

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
	.clusters {
		display: grid;
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.badge {
		margin-left: var(--space-2);
		padding: 0 var(--space-2);
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--color-info);
		background: var(--color-info-bg);
		border-radius: var(--radius-sm);
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
	}
	thead th {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
</style>
