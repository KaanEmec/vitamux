<!--
	Beat-to-beat (J22.18): one day's rr_interval samples (GET /measurements, every page), one RR
	chart per heartbeat series (the HealthKit sample the beats came from, `<series uuid>#<beat>`).
	Beats after a gap were never stored. RR intervals are a raw series: drawn as recorded, never
	resolved. Opened from an HRV reading (the point panel and the metric page); ?date= picks the day.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { addDays, clock, isDate, today } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel } from '#lib/views/format.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';
	import { beatSeries, offsetZone, type BeatSeries } from '#lib/watch/watch.ts';

	type Measurement = Schemas['Measurement'];

	const date = $derived.by(() => {
		const d = page.url.searchParams.get('date');
		return isDate(d) ? d : today();
	});

	let series = $state<BeatSeries[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);

	$effect(() => {
		const day = date;
		let stale = false;
		loading = true;
		void readAll<Measurement>(async (cursor) => {
			const res = await api.GET('/api/v1/measurements', { params: { query: { start_date: day, end_date: day, metric: ['rr_interval'], limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.measurements, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			series = beatSeries(res.items);
			problem = res.problem ?? null;
		});
		return () => (stale = true);
	});

	const iso = (t: number) => new Date(t).toISOString();
	const go = (d: string) => void goto(`?date=${d}`, { replaceState: true });
</script>

<svelte:head><title>Beat-to-beat · Vitamux</title></svelte:head>

<ViewHead
	title="Beat-to-beat"
	text="The time between heartbeats (RR intervals) that Apple Watch recorded around its HRV readings, one chart per series, as recorded."
	tile={{ code: 'hrv_rmssd', section: 'Heart and circulation' }}
>
	<div class="stepper" role="group" aria-label="Day">
		<button class="btn ghost" type="button" aria-label="Previous day" onclick={() => go(addDays(date, -1))}>←</button>
		<span>{dayLabel(date)}</span>
		<button class="btn ghost" type="button" aria-label="Next day" disabled={date >= today()} onclick={() => go(addDays(date, 1))}>→</button>
	</div>
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading beats" />
{:else if !series.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No beat-to-beat series on this day" text="Turn on the Beat-to-beat group in Apple Health in the Vitamux app to send them from Apple Watch." />{/if}
{:else}
	{#each series as s, i (s.id)}
		<section class="card" aria-labelledby="series-{i}">
			<h2 id="series-{i}">Series {i + 1} · {clock(iso(s.start), s.offset)}–{clock(iso(s.end), s.offset)}</h2>
			<p class="muted">{s.ys.length} intervals · {s.source}</p>
			{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
				<TimeSeries series={[{ label: 'RR interval', xs: s.xs, ys: s.ys }]} label="RR intervals of series {i + 1}" unit="ms" timezone={offsetZone(s.offset)} height={200} zoom={false} />
			{/await}
		</section>
	{/each}
{/if}

<style>
	.stepper {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-sm);
	}
	.card + .card {
		margin-top: var(--space-4);
	}
	h2 {
		margin: 0;
		font-size: var(--text-lg);
	}
	p {
		margin: var(--space-1) 0 var(--space-3);
		font-size: var(--text-sm);
	}
</style>
