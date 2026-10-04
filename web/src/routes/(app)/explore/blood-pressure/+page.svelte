<!--
	Blood pressure (J23.8): one dumbbell per reading session (systolic filled, diastolic hollow;
	readings within half an hour are one session and its mean is plotted), a morning and evening
	filter, plain statistics and the readings table. The tooltip adds pulse, context and device
	where the reading has them. No categories, thresholds or colouring by value.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { clock, metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import { metricLook } from '#lib/ui/metric.ts';
	import Segmented from '#lib/ui/Segmented.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { mean, recordLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import TableCard from '#lib/views/TableCard.svelte';
	import ViewHead from '#lib/views/ViewHead.svelte';

	type Reading = Schemas['BloodPressureReading'];
	type Part = 'all' | 'morning' | 'evening';

	const look = metricLook('blood_pressure');
	const pageSize = 50;
	const session = 30 * 60_000;
	const ranges: RangeKey[] = ['1M', '3M', '1Y', 'All'];
	const labels = { '1M': '30D', '3M': '90D' };
	const means: Record<RangeKey, string> = { '1D': 'Day mean', '1W': '7-day mean', '1M': '30-day mean', '3M': '90-day mean', '1Y': '1-year mean', All: 'Mean in range' };
	const parts: { value: Part; label: string }[] = [
		{ value: 'all', label: 'All' },
		{ value: 'morning', label: 'Morning' },
		{ value: 'evening', label: 'Evening' }
	];

	let rangeKey = $state<RangeKey>('3M');
	let part = $state<Part>('all');
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
			readings = res.items;
			problem = res.problem ?? null;
			shown = pageSize;
		});
		return () => (stale = true);
	});

	const at = (r: Reading) => Date.parse(r.measured_at);
	// Local hour of the reading, from its own UTC offset: morning is before 12:00, evening 17:00 or later.
	const hour = (r: Reading) => new Date(at(r) + (r.tz_offset_min ?? 0) * 60_000).getUTCHours();
	const inPart = (r: Reading) => part === 'all' || (part === 'morning' ? hour(r) < 12 : hour(r) >= 17);
	const chosen = $derived(readings.filter(inPart));
	const pairs = $derived(chosen.filter((r) => r.systolic != null && r.diastolic != null));

	// Readings within half an hour of the previous one are one session; its mean is plotted.
	const sessions = $derived.by(() => {
		const groups: Reading[][] = [];
		for (const r of pairs) {
			const g = groups.at(-1);
			if (g && at(r) - at(g[g.length - 1]) <= session) g.push(r);
			else groups.push([r]);
		}
		const avg = (g: Reading[], f: (r: Reading) => number | null) => mean(g.flatMap((r) => f(r) ?? []));
		return groups.map((g) => ({ readings: g, t: at(g[0]), sys: avg(g, (r) => r.systolic), dia: avg(g, (r) => r.diastolic), pulse: avg(g, (r) => r.pulse) }));
	});

	const sysMean = $derived(mean(pairs.map((r) => r.systolic ?? 0)));
	const diaMean = $derived(mean(pairs.map((r) => r.diastolic ?? 0)));

	const newest = $derived(chosen.toReversed());
	const context = (r: Reading) =>
		Object.entries(r.context ?? {})
			.map(([k, v]) => `${metricLabel(k)} ${String(v)}`)
			.join(', ') || '–';

	function details(i: number) {
		const s = sessions[i];
		const first = s.readings[0];
		const ctx = context(first);
		return {
			rows: [
				...(s.pulse == null ? [] : [{ label: 'Pulse', value: `${Math.round(s.pulse)} bpm` }]),
				...(ctx === '–' ? [] : [{ label: 'Context', value: ctx }]),
				{ label: 'Source', value: recordLabel(first.source), source: first.source.provider }
			],
			note: s.readings.length > 1 ? `Mean of ${s.readings.length} readings within 30 minutes` : undefined
		};
	}
</script>

<svelte:head><title>Blood pressure · Vitamux</title></svelte:head>

<ViewHead title="Blood pressure" text="Readings shown as measured. Nothing is graded or flagged." tile={{ code: 'blood_pressure' }}>
	<RangePicker bind:value={rangeKey} options={ranges} {labels} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading readings" />
{:else if !readings.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No readings in this range" text="Readings from a connected monitor or a push source appear here." />{/if}
{:else}
	<div class="bp" style:--metric={look.color}>
		<section class="card" aria-labelledby="chart-h">
			<div class="head">
				<div>
					<h2 id="chart-h">Readings</h2>
					<p class="muted note">One dumbbell per session: systolic filled, diastolic hollow. Readings within 30 minutes are one session and its mean is plotted.</p>
				</div>
				<div class="stats">
					<dl>
						<div>
							<dt>{means[rangeKey]}</dt>
							<dd>{sysMean == null || diaMean == null ? '–' : `${Math.round(sysMean)}/${Math.round(diaMean)}`} <span class="unit">mmHg</span></dd>
						</div>
						<div>
							<dt>Readings</dt>
							<dd>{chosen.length}</dd>
						</div>
					</dl>
					<Segmented label="Time of day" options={parts} bind:value={part} onchange={() => (shown = pageSize)} />
				</div>
			</div>
			{#if sessions.length}
				{#await import('#lib/charts/RangeDumbbell.svelte') then { default: RangeDumbbell }}
					<RangeDumbbell
						xs={sessions.map((s) => s.t)}
						lo={sessions.map((s) => s.dia)}
						hi={sessions.map((s) => s.sys)}
						label="Systolic and diastolic readings"
						loLabel="Diastolic"
						hiLabel="Systolic"
						unit="mmHg"
						{details}
						actions={[{ label: 'Provenance', run: (i) => (provenanceId = sessions[i].readings[0].id) }]}
					/>
				{/await}
				<p class="muted note foot">Morning is before 12:00 and evening from 17:00, in the local time of each reading.</p>
			{:else}
				<p class="muted">No readings in this part of the day.</p>
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
	</div>
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="group" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.bp {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3) var(--space-5);
		margin-bottom: var(--space-4);
	}
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		max-width: 38rem;
		margin: 0;
		font-size: var(--text-sm);
	}
	.foot {
		margin-top: var(--space-3);
		font-size: var(--text-xs);
	}
	.stats {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3) var(--space-5);
	}
	dl {
		display: flex;
		gap: var(--space-5);
		margin: 0;
	}
	dt {
		font-size: var(--text-2xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		color: var(--color-text-faint);
	}
	dd {
		margin: var(--space-1) 0 0;
		font-size: var(--text-xl);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.unit {
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--color-text-muted);
	}
	.strong {
		font-weight: 600;
	}
</style>
