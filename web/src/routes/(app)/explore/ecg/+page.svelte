<!--
	ECG (J22.18): every ecg_recording event in the range (GET /events, every page), newest first,
	with the classification Apple's ECG app recorded, as recorded, and the average heart rate. A
	row opens the recording and its strip. Nothing is rated or coloured by result.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { clock, formatValue } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel, recordLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';
	import { classification, symptoms, text } from '#lib/watch/watch.ts';

	type HealthEvent = Schemas['HealthEvent'];

	let rangeKey = $state<RangeKey>('1Y');
	let recordings = $state<HealthEvent[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);

	$effect(() => {
		const r = rangeDates(rangeKey);
		let stale = false;
		loading = true;
		void readAll<HealthEvent>(async (cursor) => {
			const res = await api.GET('/api/v1/events', { params: { query: { start_date: r.start, end_date: r.end, code: ['ecg_recording'], limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.events, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			recordings = res.items.toSorted((a, b) => b.start_at.localeCompare(a.start_at));
			problem = res.problem ?? null;
		});
		return () => (stale = true);
	});
</script>

<svelte:head><title>ECG · Vitamux</title></svelte:head>

<ViewHead
	title="ECG"
	text="Recordings from the ECG app on Apple Watch, each with the classification Apple recorded. Vitamux shows them as recorded and does not interpret them."
	tile={{ code: 'heart_rate', section: 'Heart and circulation' }}
>
	<RangePicker bind:value={rangeKey} options={['1M', '3M', '1Y', 'All']} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="block" label="Loading recordings" />
{:else if !recordings.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No ECG recordings in this range" text="Turn on the ECG group in Apple Health in the Vitamux app to send recordings from Apple Watch." />{/if}
{:else}
	<section class="card" aria-labelledby="ecg-h">
		<h2 id="ecg-h">{recordings.length} {recordings.length === 1 ? 'recording' : 'recordings'}</h2>
		<div class="scroll">
			<table>
				<thead>
					<tr>
						<th scope="col">Recorded</th>
						<th scope="col">Classification recorded by Apple</th>
						<th scope="col" class="num">Average heart rate</th>
						<th scope="col">Symptoms</th>
						<th scope="col">Source</th>
					</tr>
				</thead>
				<tbody>
					{#each recordings as e (e.id)}
						<tr>
							<th scope="row"><a href="/explore/ecg/{e.id}">{dayLabel(e.local_date)} {clock(e.start_at, e.tz_offset_min)}</a></th>
							<td class="classification">{classification(e.level)}</td>
							<td class="num">{e.value == null ? 'Not recorded' : formatValue(e.value, 'bpm')}</td>
							<td>{symptoms(text(e.context, 'symptoms_status'))}</td>
							<td class="muted">{recordLabel(e.source)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</section>
{/if}

<style>
	h2 {
		font-size: var(--text-lg);
	}
	.scroll {
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
