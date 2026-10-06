<!--
	Activity rings (J22.18): Apple's daily activity summaries, stored as daily values of
	active_energy or move_time, exercise_time and stand_hours with Apple's goal, move mode and
	paused flag in `context` (ADR-0024; GET /measurements, Apple Health, daily values). Each day
	shows move, exercise and stand as a value against that day's goal, as text and a plain bar
	filled up to the goal. The goals are Apple's; Vitamux sets none and rates nothing.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { readAll } from '#lib/data/paging.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';
	import { ringCodes, ringDays, type Ring, type RingDay } from '#lib/watch/watch.ts';

	type Measurement = Schemas['Measurement'];

	let rangeKey = $state<RangeKey>('1W');
	let days = $state<RingDay[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);

	$effect(() => {
		const r = rangeDates(rangeKey);
		let stale = false;
		loading = true;
		void readAll<Measurement>(async (cursor) => {
			const res = await api.GET('/api/v1/measurements', {
				params: { query: { start_date: r.start, end_date: r.end, metric: ringCodes, provider: ['apple_health'], kind: ['daily_value'], limit: 500, cursor } }
			});
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.measurements, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			days = ringDays(res.items);
			problem = res.problem ?? null;
		});
		return () => (stale = true);
	});

	const columns: Ring['kind'][] = ['move', 'exercise', 'stand'];
	const titles: Record<Ring['kind'], string> = { move: 'Move', exercise: 'Exercise', stand: 'Stand' };
	/** Share of the goal the bar fills, 0 to 1 (capped at the goal). */
	const fill = (r: Ring) => (r.goal && r.goal > 0 ? Math.min(Math.max(r.value / r.goal, 0), 1) : 0);
</script>

<svelte:head><title>Activity rings · Vitamux</title></svelte:head>

<ViewHead
	title="Activity rings"
	text="Move, exercise and stand per day from Apple’s activity summaries, each against the goal set in Apple’s Activity app that day."
	tile={{ code: 'active_energy', section: 'Activity' }}
>
	<RangePicker bind:value={rangeKey} options={['1W', '1M', '3M']} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="block" label="Loading activity summaries" />
{:else if !days.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No activity summaries in this range" text="Apple Health sends one a day while the Activity group is on in the Vitamux app." />{/if}
{:else}
	<section class="card" aria-labelledby="rings-h">
		<h2 id="rings-h">{days.length} {days.length === 1 ? 'day' : 'days'}</h2>
		<p class="muted">Goals are Apple’s, as set in the Activity app on each day. {days[0].source}.</p>
		<!-- svelte-ignore a11y_no_noninteractive_tabindex (a scrollable region must be focusable) -->
		<div class="scroll" tabindex="0" role="region" aria-label="Activity rings per day">
			<table>
				<caption class="visually-hidden">Move, exercise and stand per day, each against Apple’s goal</caption>
				<thead>
					<tr>
						<th scope="col">Day</th>
						{#each columns as c (c)}<th scope="col">{titles[c]} <span class="muted">of Apple’s goal</span></th>{/each}
					</tr>
				</thead>
				<tbody>
					{#each days as d (d.date)}
						<tr>
							<th scope="row">
								{dayLabel(d.date)}
								{#if d.paused}<span class="paused">Rings paused in Apple’s Activity app</span>{/if}
							</th>
							{#each columns as c (c)}
								{@const r = d.rings.find((x) => x.kind === c)}
								<td class={c}>
									{#if r}
										<span class="value">{r.text}</span>
										{#if r.goal}<span class="bar" aria-hidden="true"><span class="filled" style:width="{fill(r) * 100}%"></span></span>{/if}
									{:else}
										<span class="muted">–</span>
									{/if}
								</td>
							{/each}
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</section>
{/if}

<style>
	h2 {
		margin: 0;
		font-size: var(--text-lg);
	}
	p {
		margin: var(--space-1) 0 var(--space-3);
		font-size: var(--text-sm);
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
		vertical-align: top;
		border-top: 1px solid var(--color-border);
	}
	thead th {
		font-weight: 500;
		white-space: nowrap;
	}
	tbody th {
		font-weight: 400;
		white-space: nowrap;
	}
	.paused {
		display: block;
		font-size: var(--text-xs);
		color: var(--color-text-muted);
		white-space: normal;
	}
	.value {
		display: block;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.bar {
		display: block;
		height: 6px;
		margin-top: var(--space-1);
		min-width: 4rem;
		border-radius: var(--radius-pill);
		background: var(--chart-grid);
		overflow: hidden;
	}
	.filled {
		display: block;
		height: 100%;
		border-radius: inherit;
		background: var(--ring);
	}
	.move {
		--ring: var(--metric-energy);
	}
	.exercise {
		--ring: var(--metric-activity);
	}
	.stand {
		--ring: var(--metric-respiratory);
	}
</style>
