<!--
	Sleep (J21.9): stages per night, bed and wake consistency, and one night across sources with
	aligned hypnograms. The resolved night comes from the rule (GET /resolved/sleep); the sessions
	behind it from GET /sleep. Select a bar in either chart to look at another night.
-->
<script lang="ts">
	import { api, type Problem } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import { stageColor, stageLabels, stageOrder } from '#lib/charts/sleep.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { addDays, clock, duration } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Badge from '#lib/ui/Badge.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel, dayMs, hm, mean, memberLabel, recordLabel, ruleTag } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import { clockText, memberStages, nightHours, nightSeconds, nightSpan, selectedSessions, type Member, type Night, type Session } from '#lib/views/sleep.ts';
	import Stats from '#lib/views/Stats.svelte';
	import ViewHead from '#lib/views/ViewHead.svelte';

	// Sleep codes of the resolved result, one stack of the stage chart each.
	const stacks = ['deep', 'light', 'rem', 'awake', 'asleep_unspecified'].map((stage) => ({
		code: `sleep_${stage === 'asleep_unspecified' ? 'unspecified' : stage}`,
		label: stageLabels[stage],
		color: stageColor(stage)
	}));

	let rangeKey = $state<RangeKey>('1M');
	let nights = $state<Night[]>([]);
	let sessions = $state<Session[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let pick = $state<string | null>(null);
	/** Sessions of the shown night with their stages; null while loading. */
	let detail = $state<Session[] | null>(null);
	let detailProblem = $state<Problem | null>(null);
	let provenanceId = $state<string | null>(null);

	const sessionsPage = (from: string, to: string, stages: boolean) =>
		readAll<Session>(async (cursor) => {
			const res = await api.GET('/api/v1/sleep', {
				params: { query: { start_date: from, end_date: to, include: stages ? ['stages'] : undefined, limit: 500, cursor } }
			});
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.sleep, next: res.data.has_more ? res.data.next_cursor : undefined };
		});

	$effect(() => {
		const r = rangeDates(rangeKey);
		const start = r.start ?? r.end;
		let stale = false;
		loading = true;
		// A night's sessions can be dated the day before (ADR-0009), so the list starts a day early.
		void Promise.all([
			api.GET('/api/v1/resolved/sleep', { params: { query: { start_date: start, end_date: r.end } } }),
			sessionsPage(addDays(start, -1), r.end, false)
		]).then(([res, list]) => {
			if (stale) return;
			loading = false;
			problem = res.error ?? list.problem ?? null;
			nights = res.data?.nights ?? [];
			sessions = list.items;
			pick = null;
		});
		return () => (stale = true);
	});

	const hasData = (n: Night) => Object.keys(nightSeconds(n)).length > 0;
	const byId = $derived(new Map(sessions.map((s) => [s.id, s])));
	const night = $derived(
		nights.find((n) => n.local_date === pick) ?? nights.findLast((n) => n.members.some((m) => m.selected)) ?? nights.at(-1)
	);

	$effect(() => {
		const d = night?.local_date;
		detail = null;
		if (!d) return;
		let stale = false;
		void sessionsPage(addDays(d, -1), d, true).then((r) => {
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
	const spans = $derived(nights.map((n) => ({ date: n.local_date, span: nightSpan(selectedSessions(n, byId)) })));
	const withData = $derived(nights.filter(hasData));
	const avg = (code: string) => mean(withData.flatMap((n) => nightSeconds(n)[code] ?? []));
	const bedWake = $derived(spans.flatMap((s) => s.span ?? []));
	const stats = $derived([
		{ k: `Mean asleep · ${withData.length} nights`, v: hm(avg('sleep_total')) },
		{ k: 'Mean deep', v: hm(avg('sleep_deep')) },
		{ k: 'Mean REM', v: hm(avg('sleep_rem')) },
		{ k: 'Mean bedtime', v: bedWake.length ? clockText(mean(bedWake.map((s) => s.bed)) ?? 0) : '–' },
		{ k: 'Nights with data', v: `${withData.length} / ${nights.length}` }
	]);

	const detailById = $derived(new Map((detail ?? []).map((s) => [s.id, s])));
	const memberSessions = (m: Member) => m.session_refs.flatMap((id) => detailById.get(id) ?? []);
	const naps = $derived(
		(detail ?? []).filter((s) => s.is_nap && s.sleep_date === night?.local_date && !night.members.some((m) => m.session_refs.includes(s.id)))
	);
	const staged = $derived(night?.members.flatMap(memberSessions).filter((s) => s.stages?.length) ?? []);
	const axis = $derived({
		from: Math.min(...staged.map((s) => Date.parse(s.start_at))),
		to: Math.max(...staged.map((s) => Date.parse(s.end_at)))
	});
	const rows = $derived(stageOrder.filter((st) => staged.some((s) => s.stages?.some((x) => x.stage === st))));

	const span = (ss: Session[]) =>
		`${clock(ss[0].start_at, ss[0].tz_offset_min)} to ${clock(ss.at(-1)?.end_at ?? ss[0].end_at, ss[0].tz_offset_min)}`;
	const pickNight = (i: number) => (pick = nights[i].local_date);
</script>

<svelte:head><title>Sleep · Vitamux</title></svelte:head>

<ViewHead title="Sleep" text="Episodes are aligned across sources before one is chosen. A night is dated by the day you woke up.">
	<RangePicker bind:value={rangeKey} options={['1W', '1M', '3M', '1Y']} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading sleep" />
{:else if !withData.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No sleep in this range" text="Nothing is stored for these nights, or no source covers them." />{/if}
{:else if night}
	<Stats items={stats} />

	<section class="card" aria-labelledby="stages-h">
		<h2 id="stages-h">Stages per night</h2>
		<p class="muted note">Hours per night, from the source the rule selected.</p>
		{#await import('#lib/charts/Bars.svelte') then { default: Bars }}
			<Bars {xs} stacks={stacked} label="Sleep stages per night, stacked" timezone="UTC" unit="h" format={(v) => hm(v * 3600)} onselect={pickNight} />
		{/await}
	</section>

	<div class="row">
		<section class="card" aria-labelledby="bed-h">
			<h2 id="bed-h">Bed and wake time</h2>
			<p class="muted note">From the start of the first session to the end of the last, in the local time of each night.</p>
			{#await import('#lib/charts/RangeBars.svelte') then { default: RangeBars }}
				<RangeBars
					{xs}
					lo={spans.map((s) => s.span?.bed ?? null)}
					hi={spans.map((s) => s.span?.wake ?? null)}
					loLabel="Bed"
					hiLabel="Wake"
					picked={nights.indexOf(night)}
					timezone="UTC"
					format={clockText}
					label="Bed and wake time per night"
					onselect={pickNight}
				/>
			{/await}
		</section>

		<section class="card" aria-labelledby="across-h">
			<h2 id="across-h">Night of {dayLabel(night.local_date)}</h2>
			<p class="muted note">Every source that recorded it, on one time axis. Select a night in either chart to change it.</p>
			<ProblemAlert problem={detailProblem} />
			{#if detail === null}
				<Skeleton variant="block" label="Loading sessions" />
			{/if}
			{#each night.members as m, i (i)}
				{@const ss = memberSessions(m)}
				{@const name = memberLabel(m)}
				<article class={['member', m.selected && 'selected']} aria-label={name}>
					<header>
						<Chip source={m.provider}>{name}</Chip>
						<Badge tone={m.selected ? 'accent' : 'neutral'}>{ruleTag(m)}</Badge>
					</header>
					{#if detail && ss.some((s) => s.stages?.length)}
						{#await import('#lib/charts/Hypnogram.svelte') then { default: Hypnogram }}
							<Hypnogram stages={memberStages(ss)} {rows} from={axis.from} to={axis.to} label="Sleep stages of {name}, {span(ss)}" />
						{/await}
					{:else if detail && ss.length}
						<p class="muted">This source reported no sleep stages.</p>
					{/if}
					<p class="muted meta">
						{#if ss.length}{span(ss)} ·{/if}
						{hm(m.values?.sleep_total)} asleep
						{#if ss.length}
							{#each ss as s (s.id)}
								<button class="btn link" type="button" onclick={() => (provenanceId = s.id)}>Provenance<span class="visually-hidden"> of {name} sleep</span></button>
							{/each}
						{/if}
					</p>
				</article>
			{/each}
			{#if naps.length}
				<h3 class="naps">Naps</h3>
				<ul class="naps">
					{#each naps as s (s.id)}
						<li>
							<Chip source={s.source.provider}>{recordLabel(s.source)}</Chip>
							{span([s])} · {duration((Date.parse(s.end_at) - Date.parse(s.start_at)) / 1000)}
							<button class="btn link" type="button" onclick={() => (provenanceId = s.id)}>Provenance<span class="visually-hidden"> of nap at {clock(s.start_at, s.tz_offset_min)}</span></button>
						</li>
					{/each}
				</ul>
			{/if}
			<p class="explain">
				{night.result.explanation}
				<a href="/rules/sleep">Change rule</a>
			</p>
		</section>
	</div>
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="sleep" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.card {
		margin-bottom: var(--space-4);
	}
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.row {
		display: grid;
		gap: var(--space-4);
	}
	@media (min-width: 64rem) {
		.row {
			grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
		}
	}
	.member {
		display: grid;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
		padding: var(--space-3);
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.member.selected {
		border-color: var(--color-accent);
	}
	.member header {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2) var(--space-3);
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		margin: 0;
		font-size: var(--text-sm);
	}
	.member p:not(.meta) {
		margin: 0;
	}
	h3.naps {
		margin: var(--space-4) 0 var(--space-2);
		font-size: var(--text-md);
	}
	ul.naps {
		display: grid;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		font-size: var(--text-sm);
		list-style: none;
	}
	.explain {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
</style>
