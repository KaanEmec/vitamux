<!--
	Sleep (J23.8): last night as a large hypnogram with its numbers, nightly stage stacks, bedtime
	and wake, the average night and a list of nights. The resolved night comes from the rule (GET
	/resolved/sleep, with the episode and the time per stage); the sessions behind the shown night
	from GET /sleep. Select a bar in a chart, or "Show this night" in the list, to look at another
	night. Every source that recorded the shown night is listed with its place in the rule.
-->
<script lang="ts">
	import { api, type Problem } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import { stageColor, stageLabels } from '#lib/charts/sleep.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { addDays } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import { metricLook } from '#lib/ui/metric.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import AverageNight from '#lib/views/AverageNight.svelte';
	import { dayMs, hm, mean, quantile } from '#lib/views/format.ts';
	import NightHero from '#lib/views/NightHero.svelte';
	import NightSources from '#lib/views/NightSources.svelte';
	import NightsList from '#lib/views/NightsList.svelte';
	import { rangeDates } from '#lib/views/range.ts';
	import { clockText, episodeSpan, nightHours, nightSeconds, type Night, type Session } from '#lib/views/sleep.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';

	const look = metricLook('sleep');
	const ranges: RangeKey[] = ['1W', '1M', '3M', '1Y'];
	const labels = { '1W': '7 nights', '1M': '30 nights', '3M': '90 nights' };

	// Sleep codes of the resolved result, one stack of the stage chart each (deep at the bottom).
	const stacks = ['deep', 'light', 'rem', 'awake', 'asleep_unspecified'].map((stage) => ({
		code: `sleep_${stage === 'asleep_unspecified' ? 'unspecified' : stage}`,
		label: stageLabels[stage],
		color: stageColor(stage)
	}));

	let rangeKey = $state<RangeKey>('1M');
	let nights = $state<Night[]>([]);
	let timezone = $state<string | undefined>();
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let pick = $state<string | null>(null);
	/** Sessions of the shown night with their stages; null while loading. */
	let detail = $state<Session[] | null>(null);
	let detailProblem = $state<Problem | null>(null);

	const sessionsPage = (from: string, to: string) =>
		readAll<Session>(async (cursor) => {
			const res = await api.GET('/api/v1/sleep', { params: { query: { start_date: from, end_date: to, include: ['stages'], limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.sleep, next: res.data.has_more ? res.data.next_cursor : undefined };
		});

	$effect(() => {
		const r = rangeDates(rangeKey);
		let stale = false;
		loading = true;
		void api.GET('/api/v1/resolved/sleep', { params: { query: { start_date: r.start ?? r.end, end_date: r.end } } }).then((res) => {
			if (stale) return;
			loading = false;
			problem = res.error ?? null;
			nights = res.data?.nights ?? [];
			timezone = res.data?.timezone;
			pick = null;
		});
		return () => (stale = true);
	});

	const withData = $derived(nights.filter((n) => Object.keys(nightSeconds(n)).length > 0));
	const night = $derived(nights.find((n) => n.local_date === pick) ?? withData.at(-1) ?? nights.at(-1));

	$effect(() => {
		const d = night?.local_date;
		detail = null;
		if (!d) return;
		let stale = false;
		// A night's sessions can be dated the day before (ADR-0009).
		void sessionsPage(addDays(d, -1), d).then((r) => {
			if (stale) return;
			detail = r.items;
			detailProblem = r.problem ?? null;
		});
		return () => (stale = true);
	});

	const xs = $derived(nights.map((n) => dayMs(n.local_date)));
	const stacked = $derived(
		stacks
			.map((s) => ({ ...s, ys: nights.map((n) => nightHours(n, s.code)) }))
			.filter((s) => s.ys.some((v) => v))
	);
	const totals = $derived(nights.map((_, i) => (stacked.some((s) => s.ys[i] != null) ? stacked.reduce((n, s) => n + (s.ys[i] ?? 0), 0) : null)));
	const meanTotal = $derived(mean(totals.filter((v) => v != null)));

	const spans = $derived(nights.map((n) => episodeSpan(n, timezone)));
	const beds = $derived(spans.flatMap((s) => s?.bed ?? []));
	const wakes = $derived(spans.flatMap((s) => s?.wake ?? []));
	const quartiles = (v: number[]): [number, number] | null => (v.length > 1 ? [quantile(v, 0.25) ?? 0, quantile(v, 0.75) ?? 0] : null);
	const bands = $derived([quartiles(beds), quartiles(wakes)].filter((b) => b != null));
	const median = (v: number[]) => {
		const m = quantile(v, 0.5);
		return m == null ? '–' : clockText(m);
	};

	const pickNight = (i: number) => (pick = nights[i].local_date);
	function show(n: Night) {
		pick = n.local_date;
		window.scrollTo({ top: 0 });
	}
</script>

<svelte:head><title>Sleep · Vitamux</title></svelte:head>

<ViewHead title="Sleep" text="Episodes are aligned across sources before one is chosen. A night is dated by the day you woke up." tile={{ code: 'sleep' }}>
	<RangePicker bind:value={rangeKey} options={ranges} {labels} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading sleep" />
{:else if !withData.length || !night}
	{#if !problem}<EmptyState icon={icons.explore} title="No sleep in this range" text="Nothing is stored for these nights, or no source covers them." />{/if}
{:else}
	<div class="sleep" style:--metric={look.color}>
		<NightHero {night} last={night === withData.at(-1)} sessions={detail} {timezone} />

		<div class="grid wide">
			<section class="card" aria-labelledby="stages-h">
				<h2 id="stages-h">Nightly stages</h2>
				<p class="muted note">Hours per night by stage, from the source the rule selected. Nights with no data have no bar.</p>
				{#await import('#lib/charts/Bars.svelte') then { default: Bars }}
					<Bars
						{xs}
						stacks={stacked}
						label="Sleep stages per night, stacked"
						timezone="UTC"
						unit="h"
						format={(v) => hm(v * 3600)}
						baseline={meanTotal == null ? undefined : { value: meanTotal, label: `Mean ${hm(meanTotal * 3600)}` }}
						picked={nights.indexOf(night)}
						onselect={pickNight}
					/>
				{/await}
			</section>

			<AverageNight nights={withData} total={nights.length} />
		</div>

		<div class="grid even">
			<section class="card" aria-labelledby="bed-h">
				<h2 id="bed-h">Bedtime and wake</h2>
				<p class="muted note">Each bar runs from going to bed to getting up, in the local time of each night.</p>
				{#await import('#lib/charts/RangeBars.svelte') then { default: RangeBars }}
					<RangeBars
						{xs}
						lo={spans.map((s) => s?.bed ?? null)}
						hi={spans.map((s) => s?.wake ?? null)}
						loLabel="Bed"
						hiLabel="Wake"
						{bands}
						bandLabel="Middle half of nights"
						picked={nights.indexOf(night)}
						timezone="UTC"
						format={clockText}
						label="Bed and wake time per night"
						onselect={pickNight}
					/>
				{/await}
				<p class="medians muted">
					<span>Median bedtime <b class="num">{median(beds)}</b></span>
					<span>Median wake <b class="num">{median(wakes)}</b></span>
				</p>
			</section>

			<NightsList nights={withData.toReversed()} {timezone} onshow={show} />
		</div>

		<NightSources {night} sessions={detail} problem={detailProblem} {timezone} />
	</div>
{/if}

<style>
	.sleep {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	.grid > :global(*) {
		min-width: 0;
	}
	@media (min-width: 64rem) {
		.wide {
			grid-template-columns: minmax(0, 3fr) minmax(0, 1fr);
		}
		.even {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	.medians {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-4);
		margin: var(--space-3) 0 0;
		font-size: var(--text-xs);
	}
	.medians b {
		font-weight: 500;
		color: var(--color-text);
	}
</style>
