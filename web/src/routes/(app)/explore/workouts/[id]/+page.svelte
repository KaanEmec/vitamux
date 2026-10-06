<!--
	One workout as its source recorded it (J22.18; GET /workouts/{id}): totals and provenance, the
	route (GET /workouts/{id}/route) as a plain path without map tiles, and its laps, intervals,
	activities, pauses and markers on one time axis (EventLanes) and as a table. A workout without a
	route (404) or without segments says so.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { clock, duration, formatValue, metricLabel } from '#lib/data/format.ts';
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import MetricTile from '#lib/ui/MetricTile.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel, recordLabel } from '#lib/views/format.ts';
	import { num, offsetZone, routePoints, segmentLanes, segmentTitle, text } from '#lib/watch/watch.ts';

	type Workout = Schemas['Workout'];
	type Segment = Schemas['WorkoutSegment'];

	const id = $derived(page.params.id ?? '');

	let workout = $state<Workout | null>(null);
	let problem = $state<Problem | null>(null);
	let route = $state<ReturnType<typeof routePoints> | undefined>(undefined);
	let routeProblem = $state<Problem | null>(null);
	let provenance = $state(false);

	$effect(() => {
		const want = id;
		workout = null;
		route = undefined;
		problem = routeProblem = null;
		void api.GET('/api/v1/workouts/{id}', { params: { path: { id: want } } }).then(({ data, error }) => {
			if (want !== id) return;
			workout = data ?? null;
			problem = error ?? null;
		});
		void api.GET('/api/v1/workouts/{id}/route', { params: { path: { id: want } } }).then(({ data, error }) => {
			if (want !== id) return;
			route = data ? routePoints(data) : null;
			routeProblem = error && error.status !== 404 ? error : null;
		});
	});

	const segments = $derived((workout?.segments ?? []).toSorted((a, b) => a.seq - b.seq));
	const zone = $derived(offsetZone(workout?.tz_offset_min));
	const at = (iso: string) => clock(iso, workout?.tz_offset_min);
	const seconds = (from: string, to: string) => (Date.parse(to) - Date.parse(from)) / 1000;
	const length = (s: Segment) => {
		if (!s.end_at) return '–';
		const sec = seconds(s.start_at, s.end_at);
		return sec < 60 ? `${Math.round(sec)} s` : `${formatValue(Math.round(sec / 6) / 10)} min`;
	};
	const total = (s: Segment, key: string) => num(s.data && (s.data as Record<string, unknown>).totals, key);
</script>

<svelte:head><title>{workout ? metricLabel(workout.sport) : 'Workout'} · Vitamux</title></svelte:head>

<nav class="crumbs" aria-label="Breadcrumb">
	<a href="/explore">Explore</a><span aria-hidden="true">/</span><a href="/explore/workouts">Workouts</a><span aria-hidden="true">/</span><span aria-current="page">Workout</span>
</nav>

{#if problem?.status === 404}
	<h1>Workout</h1>
	<EmptyState icon={icons.explore} title="No workout with this id" text="The workouts view lists every workout Vitamux has stored.">
		<Button href="/explore/workouts">Workouts</Button>
	</EmptyState>
{:else if !workout}
	<h1>Workout</h1>
	<ProblemAlert {problem} />
	{#if !problem}<Skeleton variant="block" label="Loading the workout" />{/if}
{:else}
	<header class="head">
		<MetricTile code="workouts" section="Activity" size="lg" />
		<div>
			<h1>{metricLabel(workout.sport)}</h1>
			<p class="muted">{dayLabel(workout.local_date)} {at(workout.start_at)}–{at(workout.end_at)} · {recordLabel(workout.source)}</p>
		</div>
	</header>

	<section class="card" aria-labelledby="totals-h">
		<h2 id="totals-h" class="visually-hidden">Totals</h2>
		<dl>
			<div><dt>Duration</dt><dd>{duration(seconds(workout.start_at, workout.end_at))}</dd></div>
			<div><dt>Distance</dt><dd>{workout.distance_m == null ? '–' : formatValue(workout.distance_m / 1000, 'km')}</dd></div>
			<div><dt>Energy</dt><dd>{formatValue(workout.energy_kcal, 'kcal')}</dd></div>
			<div><dt>Average / max heart rate</dt><dd>{formatValue(workout.avg_hr_bpm)} / {formatValue(workout.max_hr_bpm)} bpm</dd></div>
		</dl>
		<button class="btn link" type="button" onclick={() => (provenance = true)}>Provenance<span class="visually-hidden"> of this workout</span></button>
	</section>

	<section class="card" aria-labelledby="route-h">
		<h2 id="route-h">Route</h2>
		<ProblemAlert problem={routeProblem} />
		{#if route === undefined}
			<Skeleton variant="chart" label="Loading the route" />
		{:else if route === null}
			{#if !routeProblem}<p class="muted">No route was recorded for this workout.</p>{/if}
		{:else}
			{#await import('#lib/charts/RoutePath.svelte') then { default: RoutePath }}
				<RoutePath points={route.points} count={route.count} label="Route of this workout" timezone={zone} />
			{/await}
			<p class="muted note">{route.count.toLocaleString()} locations as recorded{route.points.length < route.count ? `, ${route.points.length.toLocaleString()} drawn` : ''}. Drawn as a plain path: the panel loads no map tiles, so the route stays on your server.</p>
		{/if}
	</section>

	<section class="card" aria-labelledby="segments-h">
		<h2 id="segments-h">Laps and activities</h2>
		{#if !segments.length}
			<p class="muted">No laps, activities, pauses or markers were recorded.</p>
		{:else}
			{#await import('#lib/charts/EventLanes.svelte') then { default: EventLanes }}
				<EventLanes lanes={segmentLanes(segments)} from={Date.parse(workout.start_at)} to={Date.parse(workout.end_at)} label="Segments over the workout" timezone={zone} />
			{/await}
			<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be focusable) -->
			<div class="scroll" tabindex="0" role="region" aria-label="Segments of the workout">
				<table>
					<thead>
						<tr><th scope="col">Segment</th><th scope="col">Time</th><th scope="col" class="num">Length</th><th scope="col" class="num">Distance</th><th scope="col" class="num">Energy</th><th scope="col">Location</th></tr>
					</thead>
					<tbody>
						{#each segments as s (s.seq)}
							{@const distance = total(s, 'distance_m')}
							<tr>
								<th scope="row">{segmentTitle(s, segments)}</th>
								<td>{at(s.start_at)}{s.end_at ? `–${at(s.end_at)}` : ''}</td>
								<td class="num">{length(s)}</td>
								<td class="num">{distance == null ? '–' : formatValue(distance / 1000, 'km')}</td>
								<td class="num">{formatValue(total(s, 'energy_kcal'), 'kcal')}</td>
								<td>{text(s.data, 'location') ? metricLabel(text(s.data, 'location') ?? '') : '–'}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>

	{#if provenance}
		<ProvenanceDialog entity="workout" id={workout.id} onclose={() => (provenance = false)} />
	{/if}
{/if}

<style>
	.crumbs {
		display: flex;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.crumbs a {
		text-decoration: none;
	}
	[aria-current] {
		color: var(--color-text);
	}
	.head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		margin-bottom: var(--space-5);
	}
	h1 {
		margin: 0;
		letter-spacing: var(--tracking-tight);
	}
	.head p {
		margin: var(--space-1) 0 0;
	}
	.card + .card {
		margin-top: var(--space-4);
	}
	h2 {
		font-size: var(--text-lg);
	}
	dl {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
		gap: var(--space-3) var(--space-5);
		margin: 0 0 var(--space-3);
	}
	dt {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
	}
	.note {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
	}
	.scroll {
		margin-top: var(--space-3);
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
</style>
