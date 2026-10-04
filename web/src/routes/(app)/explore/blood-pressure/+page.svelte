<!--
	Blood pressure (J21.9): each reading keeps systolic, diastolic and pulse together, shown as
	measured. Dumbbells for systolic and diastolic, a pulse line, plain statistics and the
	readings table. No categories, thresholds or colouring by value.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { addDays, clock, metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { mean, recordLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import Stats from '#lib/views/Stats.svelte';
	import TableCard from '#lib/views/TableCard.svelte';
	import ViewHead from '#lib/views/ViewHead.svelte';

	type Reading = Schemas['BloodPressureReading'];

	const pageSize = 50;

	let rangeKey = $state<RangeKey>('3M');
	let end = $state('');
	let readings = $state<Reading[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let shown = $state(pageSize);
	let provenanceId = $state<string | null>(null);

	$effect(() => {
		const r = rangeDates(rangeKey);
		let stale = false;
		loading = true;
		void readAll<Reading>(async (cursor) => {
			const res = await api.GET('/api/v1/blood-pressure', { params: { query: { start_date: r.start, end_date: r.end, limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.readings, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			end = r.end;
			readings = res.items;
			problem = res.problem ?? null;
			shown = pageSize;
		});
		return () => (stale = true);
	});

	const pairs = $derived(readings.filter((r) => r.systolic != null && r.diastolic != null));
	const pulses = $derived(readings.filter((r) => r.pulse != null));
	const at = (r: Reading) => Date.parse(r.measured_at);

	// Plain means of the readings in the last N days of the range.
	function windowMean(days: number): string {
		const from = addDays(end, 1 - days);
		const rs = pairs.filter((r) => r.local_date >= from);
		const [s, d] = [mean(rs.map((r) => r.systolic ?? 0)), mean(rs.map((r) => r.diastolic ?? 0))];
		return s == null || d == null ? '–' : `${Math.round(s)}/${Math.round(d)}`;
	}
	const pulseMean = $derived(mean(pulses.map((r) => r.pulse ?? 0)));
	const stats = $derived([
		{ k: '7-day mean', v: windowMean(7), u: 'mmHg' },
		{ k: '30-day mean', v: windowMean(30), u: 'mmHg' },
		{ k: 'Readings in range', v: String(readings.length) },
		{ k: 'Pulse mean in range', v: pulseMean == null ? '–' : String(Math.round(pulseMean)), u: 'bpm' }
	]);

	const newest = $derived([...readings].reverse());
	const context = (r: Reading) =>
		Object.entries(r.context ?? {})
			.map(([k, v]) => `${metricLabel(k)} ${String(v)}`)
			.join(', ') || '–';
</script>

<svelte:head><title>Blood pressure · Vitamux</title></svelte:head>

<ViewHead title="Blood pressure" text="Each reading keeps systolic, diastolic and pulse together, shown as measured, without categories.">
	<RangePicker bind:value={rangeKey} options={['1W', '1M', '3M', '1Y', 'All']} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading readings" />
{:else if !readings.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No readings in this range" text="Readings from a connected monitor or a push source appear here." />{/if}
{:else}
	<Stats items={stats} />

	<section class="card" aria-labelledby="chart-h">
		<h2 id="chart-h">Readings</h2>
		{#await import('#lib/charts/RangeDumbbell.svelte') then { default: RangeDumbbell }}
			<RangeDumbbell
				xs={pairs.map(at)}
				lo={pairs.map((r) => r.diastolic)}
				hi={pairs.map((r) => r.systolic)}
				label="Systolic and diastolic readings"
				loLabel="Diastolic"
				hiLabel="Systolic"
				unit="mmHg"
			/>
		{/await}
		{#if pulses.length}
			<h3>Pulse</h3>
			{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
				<TimeSeries series={[{ label: 'Pulse', xs: pulses.map(at), ys: pulses.map((r) => r.pulse) }]} label="Pulse of the same readings" unit="bpm" height={160} zoom={false} />
			{/await}
		{/if}
	</section>

	<TableCard title="Readings table" remaining={newest.length - shown} onmore={() => (shown += pageSize)}>
		<thead>
			<tr>
				<th scope="col">Measured</th><th scope="col" class="num">Systolic</th><th scope="col" class="num">Diastolic</th>
				<th scope="col" class="num">Pulse</th><th scope="col">Context</th><th scope="col">Source</th><th scope="col"><span class="visually-hidden">Provenance</span></th>
			</tr>
		</thead>
		<tbody>
			{#each newest.slice(0, shown) as r (r.id)}
				<tr>
					<th scope="row">{r.local_date} {clock(r.measured_at, r.tz_offset_min)}</th>
					<td class="num strong">{r.systolic ?? '–'}</td>
					<td class="num strong">{r.diastolic ?? '–'}</td>
					<td class="num">{r.pulse ?? '–'}</td>
					<td>{context(r)}</td>
					<td><Chip source={r.source.provider}>{recordLabel(r.source)}</Chip></td>
					<td>
						<button class="btn link" type="button" onclick={() => (provenanceId = r.id)}>
							Provenance<span class="visually-hidden"> of the reading on {r.local_date} {clock(r.measured_at, r.tz_offset_min)}</span>
						</button>
					</td>
				</tr>
			{/each}
		</tbody>
	</TableCard>
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="group" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.card {
		margin-bottom: var(--space-4);
	}
	h2 {
		margin-bottom: var(--space-3);
	}
	h3 {
		margin: var(--space-4) 0 var(--space-2);
		font-size: var(--text-md);
		color: var(--color-text-muted);
	}
	.strong {
		font-weight: 600;
	}
</style>
