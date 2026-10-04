<!--
	Workouts (J21.9): a month calendar and the month's workouts as clusters. Workouts that overlap
	in time are listed together, one row per source, so duplicates across sources are visible; the
	rule's pick is marked (GET /resolved/workouts). Pick a day in the calendar to list only that day.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { duration, formatValue, metricLabel, today } from '#lib/data/format.ts';
	import Badge from '#lib/ui/Badge.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import Calendar, { monthRange } from '#lib/views/Calendar.svelte';
	import { memberLabel, ruleTag } from '#lib/views/format.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';

	type Cluster = Schemas['ResolvedWorkout'];
	type Member = Schemas['WorkoutMember'];

	let month = $state(today().slice(0, 7));
	let picked = $state<string | null>(null);
	let clusters = $state<Cluster[]>([]);
	let timezone = $state<string | undefined>();
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let provenanceId = $state<string | null>(null);

	$effect(() => {
		const { start, end } = monthRange(month);
		let stale = false;
		loading = true;
		picked = null;
		void api.GET('/api/v1/resolved/workouts', { params: { query: { start_date: start, end_date: end } } }).then(({ data, error }) => {
			if (stale) return;
			loading = false;
			problem = error ?? null;
			clusters = data?.workouts ?? [];
			timezone = data?.timezone;
		});
		return () => (stale = true);
	});

	const counts = $derived(clusters.reduce<Record<string, number>>((n, c) => ({ ...n, [c.local_date]: (n[c.local_date] ?? 0) + 1 }), {}));
	const listed = $derived(clusters.filter((c) => !picked || c.local_date === picked).toReversed());

	const clock = (iso: string) => new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', timeZone: timezone }).format(Date.parse(iso));
	const seconds = (m: Member) => (Date.parse(m.end_at) - Date.parse(m.start_at)) / 1000;
	const num = (v: number | undefined, unit?: string) => (v == null ? '–' : formatValue(v, unit));
</script>

<svelte:head><title>Workouts · Vitamux</title></svelte:head>

<ViewHead title="Workouts" text="Workouts that overlap in time are listed together, one row per source, so duplicates across sources are visible." />

<ProblemAlert {problem} />

<div class="layout">
	<section class="card" aria-label="Calendar">
		<Calendar {month} {counts} {picked} unit="workout" onmonth={(m) => (month = m)} onpick={(d) => (picked = d)} />
	</section>

	<div class="list">
		{#if loading}
			<Skeleton variant="block" label="Loading workouts" />
		{:else if !listed.length}
			{#if !problem}<EmptyState icon={icons.explore} title={picked ? 'No workouts on this day' : 'No workouts in this month'} text="Workouts from every connected source appear here." />{/if}
		{:else}
			{#if picked}<p class="muted">Showing {picked}. Pick the day again in the calendar to show the whole month.</p>{/if}
			<ul>
				{#each listed as c (c.start + c.sport + c.members[0]?.id)}
					<li class="card">
						<h2>
							{metricLabel(c.sport)}
							<span class="muted">· {c.local_date} {clock(c.start)}–{clock(c.end)}</span>
							{#if c.members.length > 1}<Badge tone="accent">{c.members.length} sources</Badge>{/if}
						</h2>
						<div class="scroll">
							<table>
								<thead>
									<tr>
										<th scope="col">Source</th><th scope="col">Duration</th><th scope="col" class="num">Distance</th>
										<th scope="col" class="num">Energy</th><th scope="col" class="num">Avg / max HR</th><th scope="col">Rule</th><th scope="col">Provenance</th>
									</tr>
								</thead>
								<tbody>
									{#each c.members as m (m.id)}
										<tr>
											<th scope="row"><Chip source={m.provider}>{memberLabel(m)}</Chip></th>
											<td>{duration(seconds(m))}</td>
											<td class="num">{m.distance_m == null ? '–' : formatValue(m.distance_m / 1000, 'km')}</td>
											<td class="num">{num(m.energy_kcal, 'kcal')}</td>
											<td class="num">{num(m.avg_hr_bpm)} / {num(m.max_hr_bpm)} bpm</td>
											<td><Badge tone={m.selected ? 'accent' : 'neutral'}>{ruleTag(m)}</Badge></td>
											<td>
												<button class="btn link" type="button" onclick={() => (provenanceId = m.id)}>
													Provenance<span class="visually-hidden"> of {memberLabel(m)} workout</span>
												</button>
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
						<p class="muted explain">{c.explanation}</p>
					</li>
				{/each}
			</ul>
		{/if}
	</div>
</div>

{#if provenanceId}
	<ProvenanceDialog entity="workout" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		align-items: start;
	}
	@media (min-width: 64rem) {
		.layout {
			grid-template-columns: minmax(18rem, 22rem) minmax(0, 1fr);
		}
	}
	ul {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.list > p {
		margin: 0 0 var(--space-3);
	}
	h2 {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-lg);
	}
	.scroll {
		position: relative;
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
		white-space: nowrap;
		border-top: 1px solid var(--color-border);
	}
	thead th {
		font-weight: 500;
		color: var(--color-text-muted);
	}
	tbody th {
		font-weight: 400;
	}
	.num {
		text-align: right;
	}
	.explain {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
	}
</style>
