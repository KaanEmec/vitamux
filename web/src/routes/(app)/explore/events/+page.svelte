<!--
	Events (J21.9): health events (alerts, symptoms and other typed events) as one lane per event
	type on a shared time axis, grouped by family (J22.18: heart rhythm and rate, mind, cycle
	tracking, symptoms, other alerts, other events; lib/watch/watch.ts). The type picker groups the
	same way. `?code=` shows a single type. The data behind the lanes is also available as a table.
	An ECG lane links to the ECG view, a route lane to the workouts. Levels are shown as recorded.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayMs } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';
	import { byFamily, classification, familyTitle } from '#lib/watch/watch.ts';

	type HealthEvent = Schemas['HealthEvent'];

	const day = 86_400_000;

	let rangeKey = $state<RangeKey>('3M');
	let events = $state<HealthEvent[]>([]);
	let codes = $state<{ code: string; count: number }[]>([]);
	let span = $state<[number, number]>([0, 1]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);

	const code = $derived(page.url.searchParams.get('code'));

	$effect(() => {
		void api.GET('/api/v1/inventory').then(({ data }) => {
			codes = (data?.items ?? []).filter((i) => i.kind === 'event').map((i) => ({ code: i.code, count: i.count }));
		});
	});

	$effect(() => {
		const r = rangeDates(rangeKey);
		const only = code ? [code] : undefined;
		let stale = false;
		loading = true;
		void readAll<HealthEvent>(async (cursor) => {
			const res = await api.GET('/api/v1/events', { params: { query: { start_date: r.start, end_date: r.end, code: only, limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.events, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			events = res.items;
			problem = res.problem ?? null;
			span = [r.start ? dayMs(r.start) : Math.min(...res.items.map((e) => Date.parse(e.start_at))), dayMs(r.end) + day];
		});
		return () => (stale = true);
	});

	const label = (e: HealthEvent) => {
		if (e.code === 'ecg_recording') return classification(e.level);
		if (e.level) return metricLabel(e.level);
		return e.value == null ? metricLabel(e.code) : String(e.value);
	};

	const groups = $derived(
		byFamily(Object.entries(Object.groupBy(events, (e) => e.code)).map(([c, list = []]) => ({ code: c, list }))).map((g) => ({
			family: g.family,
			lanes: g.items.map(({ code: c, list }) => ({
				label: metricLabel(c),
				events: list.map((e) => {
					const start = Date.parse(e.start_at);
					return { start, end: e.end_at ? Date.parse(e.end_at) : start, label: label(e) };
				})
			})),
			codes: g.items.map((i) => i.code)
		}))
	);
	const typeCount = $derived(groups.reduce((n, g) => n + g.lanes.length, 0));
	const pickerGroups = $derived(byFamily(codes));

	function choose(e: { currentTarget: HTMLSelectElement }) {
		const value = e.currentTarget.value;
		void goto(value ? `?code=${encodeURIComponent(value)}` : page.url.pathname, { replaceState: true });
	}
</script>

<svelte:head><title>Events · Vitamux</title></svelte:head>

<ViewHead title="Events" text="Alerts, symptoms and other typed events, one lane per type. A bar spans an event from start to end." tile={{ code: 'events' }}>
	<RangePicker bind:value={rangeKey} options={['1M', '3M', '1Y', 'All']} />
</ViewHead>

<div class="field">
	<label for="code">Event type</label>
	<select id="code" value={code ?? ''} onchange={choose}>
		<option value="">All types</option>
		{#each pickerGroups as g (g.family)}
			<optgroup label={familyTitle[g.family]}>
				{#each g.items as c (c.code)}<option value={c.code}>{metricLabel(c.code)} ({c.count})</option>{/each}
			</optgroup>
		{/each}
		{#if code && !codes.some((c) => c.code === code)}<option value={code}>{metricLabel(code)}</option>{/if}
	</select>
</div>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading events" />
{:else if !events.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No events in this range" text="Events from connected sources appear here." />{/if}
{:else}
	<h2 class="count">{events.length} {events.length === 1 ? 'event' : 'events'} in {typeCount} {typeCount === 1 ? 'type' : 'types'}</h2>
	{#each groups as g (g.family)}
		<section class="card" aria-labelledby="family-{g.family}">
			<div class="family-head">
				<h3 id="family-{g.family}">{familyTitle[g.family]}</h3>
				{#if g.codes.includes('ecg_recording')}<a href="/explore/ecg">ECG recordings and their strips</a>{/if}
				{#if g.codes.includes('workout_route')}<a href="/explore/workouts">Workouts and their routes</a>{/if}
			</div>
			{#await import('#lib/charts/EventLanes.svelte') then { default: EventLanes }}
				<EventLanes lanes={g.lanes} from={span[0]} to={span[1]} label="Events by type over time: {familyTitle[g.family]}" />
			{/await}
		</section>
	{/each}
{/if}

<style>
	.field {
		max-width: 22rem;
	}
	.count {
		font-size: var(--text-lg);
	}
	.card + .card {
		margin-top: var(--space-4);
	}
	.family-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2) var(--space-4);
		margin-bottom: var(--space-2);
		font-size: var(--text-sm);
	}
	h3 {
		margin: 0;
		font-size: var(--text-md);
	}
</style>
