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
	import StageStack from '#lib/charts/StageStack.svelte';
	import { stageColor, stageLabels, stageOrder } from '#lib/charts/sleep.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { addDays, clock, duration } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Badge from '#lib/ui/Badge.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import { metricLook } from '#lib/ui/metric.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { sourceClass } from '#lib/ui/source.ts';
	import { dayLabel, dayMs, hm, mean, memberLabel, quantile, recordLabel, ruleTag } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import {
		clockText,
		episodeSpan,
		memberStages,
		nightHours,
		nightLabel,
		nightSeconds,
		shortDay,
		type Member,
		type Night,
		type Session
	} from '#lib/views/sleep.ts';
	import ViewHead from '#lib/views/ViewHead.svelte';

	const look = metricLook('sleep');
	const ranges: RangeKey[] = ['1W', '1M', '3M', '1Y'];
	const labels = { '1W': '7 nights', '1M': '30 nights', '3M': '90 nights' };
	const pageSize = 7;

	// Sleep codes of the resolved result, one stack of the stage chart each (deep at the bottom).
	const stacks = ['deep', 'light', 'rem', 'awake', 'asleep_unspecified'].map((stage) => ({
		code: `sleep_${stage === 'asleep_unspecified' ? 'unspecified' : stage}`,
		label: stageLabels[stage],
		color: stageColor(stage)
	}));
	// The four stages of the hero, the average night and the list, in the canvas order.
	const stages = ['deep', 'rem', 'light', 'awake'] as const;

	let rangeKey = $state<RangeKey>('1M');
	let nights = $state<Night[]>([]);
	let timezone = $state<string | undefined>();
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let pick = $state<string | null>(null);
	let shown = $state(pageSize);
	/** Sessions of the shown night with their stages; null while loading. */
	let detail = $state<Session[] | null>(null);
	let detailProblem = $state<Problem | null>(null);
	let provenanceId = $state<string | null>(null);

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
			shown = pageSize;
		});
		return () => (stale = true);
	});

	const hasData = (n: Night) => Object.keys(nightSeconds(n)).length > 0;
	const withData = $derived(nights.filter(hasData));
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

	// ---- the shown night -----------------------------------------------------------------
	const secs = $derived(night ? nightSeconds(night) : {});
	const stageSum = $derived(stages.reduce((n, s) => n + (secs[`sleep_${s}`] ?? 0), 0));
	const efficiency = $derived(secs.sleep_total && secs.sleep_in_bed ? Math.round((secs.sleep_total / secs.sleep_in_bed) * 100) : null);
	const span = $derived(night ? episodeSpan(night, timezone) : null);
	const detailById = $derived(new Map((detail ?? []).map((s) => [s.id, s])));
	const memberSessions = (m: Member) => m.session_refs.flatMap((id) => detailById.get(id) ?? []);
	const picked = $derived(night?.members.find((m) => m.selected));
	const pickedSessions = $derived(picked ? memberSessions(picked) : []);
	const naps = $derived(
		(detail ?? []).filter((s) => s.is_nap && s.sleep_date === night?.local_date && !night.members.some((m) => m.session_refs.includes(s.id)))
	);

	/** The stage rows and time axis that line a set of sessions up. */
	function bounds(ss: Session[]) {
		const staged = ss.filter((s) => s.stages?.length);
		return {
			staged,
			from: Math.min(...staged.map((s) => Date.parse(s.start_at))),
			to: Math.max(...staged.map((s) => Date.parse(s.end_at))),
			rows: stageOrder.filter((st) => staged.some((s) => s.stages?.some((x) => x.stage === st)))
		};
	}
	const heroAxis = $derived(bounds(pickedSessions));
	const allAxis = $derived(bounds(night?.members.flatMap(memberSessions) ?? []));

	const range = (ss: Session[]) =>
		`${clock(ss[0].start_at, ss[0].tz_offset_min)} to ${clock(ss.at(-1)?.end_at ?? ss[0].end_at, ss[0].tz_offset_min)}`;
	const pickNight = (i: number) => (pick = nights[i].local_date);

	// ---- the period ----------------------------------------------------------------------
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

	// The average night: the mean time in each stage, and the range of its share of the night.
	const average = $derived.by(() => {
		const per = withData.map(nightSeconds).filter((s) => stages.some((st) => s[`sleep_${st}`] != null));
		const shares = (st: string) => per.flatMap((s) => (s[`sleep_${st}`] != null ? s[`sleep_${st}`] / stages.reduce((n, o) => n + (s[`sleep_${o}`] ?? 0), 0) : []));
		const rows = stages.map((st) => {
			const sh = shares(st);
			return { stage: st, label: stageLabels[st], seconds: mean(per.flatMap((s) => s[`sleep_${st}`] ?? [])) ?? 0, share: mean(sh) ?? 0, lo: Math.min(...sh), hi: Math.max(...sh) };
		});
		const top = Math.max(0.1, Math.ceil(Math.max(...rows.map((r) => (Number.isFinite(r.hi) ? r.hi : 0))) * 10) / 10);
		return { rows: per.length ? rows : [], top };
	});
	const percent = (v: number) => `${Math.round(v * 100)}%`;

	// ---- the list ------------------------------------------------------------------------
	const listed = $derived(withData.toReversed());
	/** "WHOOP · used" for the selected source, "Garmin · 7h 11m" for another with a total. */
	const chipText = (m: Member) => `${memberLabel(m)}${m.selected ? ' · used' : m.values?.sleep_total ? ` · ${hm(m.values.sleep_total)}` : ''}`;
	const others = (n: Night) => n.members.filter((m) => !m.selected);
	const sourceText = (n: Night) => {
		const used = n.members.find((m) => m.selected);
		const rest = others(n).map((m) => memberLabel(m));
		return `${used ? memberLabel(used) : 'No source in the rule'}${rest.length ? ` · ${rest.join(', ')} also recorded` : ''}`;
	};
	const bedWake = (n: Night) => {
		const s = episodeSpan(n, timezone);
		return s ? `${clockText(s.bed)} → ${clockText(s.wake)}` : '';
	};
	const stageSeconds = (n: Night) => stages.map((st) => ({ stage: st, seconds: nightSeconds(n)[`sleep_${st}`] ?? 0 }));
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
		<section class="card hero" aria-labelledby="hero-h">
			<div class="summary">
				<h2 id="hero-h" class="lbl">{night === withData.at(-1) ? 'Last night' : 'Night'} · {nightLabel(night.local_date)}</h2>
				<p class="big">{hm(secs.sleep_total)}</p>
				<dl class="facts">
					<div><dt>In bed</dt><dd>{hm(secs.sleep_in_bed)}</dd></div>
					<div><dt>Efficiency</dt><dd>{efficiency == null ? '–' : `${efficiency}%`}</dd></div>
					<div><dt>Bedtime</dt><dd>{span ? clockText(span.bed) : '–'}</dd></div>
					<div><dt>Wake</dt><dd>{span ? clockText(span.wake) : '–'}</dd></div>
				</dl>
				<div class="chips">
					{#each night.members as m, i (i)}
						<Chip source={m.provider} dashed={!m.selected}>{chipText(m)}</Chip>
					{/each}
				</div>
			</div>
			<div class="detail">
				{#if detail === null}
					<Skeleton variant="block" label="Loading sessions" />
				{:else if heroAxis.staged.length}
					{#await import('#lib/charts/Hypnogram.svelte') then { default: Hypnogram }}
						<Hypnogram
							stages={memberStages(heroAxis.staged)}
							rows={heroAxis.rows}
							from={heroAxis.from}
							to={heroAxis.to}
							rowHeight={36}
							{timezone}
							label="Sleep stages of {picked ? memberLabel(picked) : 'the night'}, {range(heroAxis.staged)}"
						/>
					{/await}
				{:else}
					<p class="muted">No stage detail for this night.</p>
				{/if}
				<ul class="stages">
					{#each stages as st (st)}
						{@const v = secs[`sleep_${st}`]}
						<li>
							<span class="row"><span class="muted">{stageLabels[st]}</span><span class="num">{hm(v)}</span></span>
							<span class="track"><span class={['fill', stageColor(st)]} style:width="{stageSum && v ? (v / stageSum) * 100 : 0}%"></span></span>
						</li>
					{/each}
				</ul>
			</div>
		</section>

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

			<section class="card" aria-labelledby="avg-h">
				<h2 id="avg-h">Average night</h2>
				<p class="muted note">{withData.length} of {nights.length} nights</p>
				{#if average.rows.length}
					<StageStack stages={average.rows.map((r) => ({ stage: r.stage, seconds: r.seconds }))} label="Average night by stage" legend={false} />
					<ul class="avg">
						{#each average.rows as r (r.stage)}
							<li>
								<span class="row"><span class="name"><span class={['swatch', stageColor(r.stage)]}></span>{r.label}</span><span class="num">{hm(r.seconds)} <span class="muted">· {percent(r.share)}</span></span></span>
								<span class="track">
									<span class={['fill range', stageColor(r.stage)]} style:left="{(r.lo / average.top) * 100}%" style:width="{Math.max(2, ((r.hi - r.lo) / average.top) * 100)}%"></span>
									<span class={['tick', stageColor(r.stage)]} style:left="{(r.share / average.top) * 100}%"></span>
								</span>
							</li>
						{/each}
					</ul>
					<p class="muted note">Bar = range of its share of the night across the period, tick = mean.</p>
				{/if}
			</section>
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
						means={false}
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

			<section class="card list" aria-labelledby="nights-h">
				<h2 id="nights-h">Nights</h2>
				<ul>
					{#each listed.slice(0, shown) as n (n.local_date)}
						{@const used = n.members.find((m) => m.selected)}
						<li>
							<details>
								<summary>
									<span class="when"><b>{shortDay(n.local_date)}</b><span class="num muted">{bedWake(n)}</span></span>
									<span class="mid">
										<StageStack stages={stageSeconds(n)} label="Stages of the night of {dayLabel(n.local_date)}" legend={false} />
										<span class="src muted"><span class={['dot', used && sourceClass(used.provider)]}></span>{sourceText(n)}</span>
									</span>
									<span class="dur num">{hm(nightSeconds(n).sleep_total)}</span>
								</summary>
								<div class="more">
									<StageStack stages={stageSeconds(n)} label="Time per stage of the night of {dayLabel(n.local_date)}" />
									<ul class="members">
										{#each n.members as m, i (i)}
											<li><Chip source={m.provider} dashed={!m.selected}>{memberLabel(m)}</Chip><span class="muted">{ruleTag(m)} · {hm(m.values?.sleep_total)} asleep</span></li>
										{/each}
									</ul>
									<button class="btn sm" type="button" onclick={() => show(n)}>Show this night<span class="visually-hidden"> of {dayLabel(n.local_date)}</span></button>
								</div>
							</details>
						</li>
					{/each}
				</ul>
				{#if listed.length > shown}
					<p><button class="btn sm" type="button" onclick={() => (shown += pageSize)}>Show more ({listed.length - shown} left)</button></p>
				{/if}
			</section>
		</div>

		<section class="card" aria-labelledby="across-h">
			<h2 id="across-h">Sources for the night of {dayLabel(night.local_date)}</h2>
			<p class="muted note">Every source that recorded it, on one time axis, and where each stands under the rule.</p>
			<ProblemAlert problem={detailProblem} />
			<div class="members-grid">
				{#each night.members as m, i (i)}
					{@const ss = memberSessions(m)}
					{@const name = memberLabel(m)}
					<article class={['member', m.selected && 'selected']} aria-label={name}>
						<header>
							<Chip source={m.provider}>{name}</Chip>
							<Badge tone={m.selected ? 'accent' : 'neutral'}>{ruleTag(m)}</Badge>
						</header>
						{#if detail && !m.selected}
							{#if ss.some((s) => s.stages?.length)}
								{#await import('#lib/charts/Hypnogram.svelte') then { default: Hypnogram }}
									<Hypnogram stages={memberStages(ss)} rows={allAxis.rows} from={allAxis.from} to={allAxis.to} {timezone} label="Sleep stages of {name}, {range(ss)}" />
								{/await}
							{:else if ss.length}
								<p class="muted">This source reported no sleep stages.</p>
							{/if}
						{/if}
						<p class="muted meta">
							{#if ss.length}{range(ss)} ·{/if}
							{hm(m.values?.sleep_total)} asleep
							{#each ss as s (s.id)}
								<button class="btn link" type="button" onclick={() => (provenanceId = s.id)}>Provenance<span class="visually-hidden"> of {name} sleep</span></button>
							{/each}
						</p>
					</article>
				{/each}
			</div>
			{#if naps.length}
				<h3 class="naps">Naps</h3>
				<ul class="naps">
					{#each naps as s (s.id)}
						<li>
							<Chip source={s.source.provider}>{recordLabel(s.source)}</Chip>
							{range([s])} · {duration((Date.parse(s.end_at) - Date.parse(s.start_at)) / 1000)}
							<button class="btn link" type="button" onclick={() => (provenanceId = s.id)}>Provenance<span class="visually-hidden"> of nap at {clock(s.start_at, s.tz_offset_min)}</span></button>
						</li>
					{/each}
				</ul>
			{/if}
			<p class="explain muted">
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
	.lbl {
		margin: 0;
		font-size: var(--text-2xs);
		font-weight: 500;
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		color: var(--color-text-faint);
	}
	.hero {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-5) var(--space-6);
	}
	.summary {
		display: flex;
		flex: 1 1 14rem;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
	}
	.big {
		margin: 0;
		font-size: var(--text-display);
		font-weight: 600;
		letter-spacing: -0.03em;
		font-variant-numeric: tabular-nums;
	}
	.facts {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-3) var(--space-4);
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
		font-size: var(--text-lg);
		font-weight: 500;
		font-variant-numeric: tabular-nums;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.detail {
		display: flex;
		flex: 999 1 22rem;
		flex-direction: column;
		gap: var(--space-4);
		min-width: 0;
	}
	.stages,
	.avg,
	.list ul,
	.members,
	ul.naps {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.stages {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(7rem, 1fr));
		gap: var(--space-3);
	}
	.row {
		display: flex;
		justify-content: space-between;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
		font-size: var(--text-xs);
	}
	.track {
		position: relative;
		display: block;
		height: 0.375rem;
		background: var(--chart-grid);
		border-radius: var(--radius-pill);
	}
	.fill {
		position: absolute;
		top: 0;
		bottom: 0;
		left: 0;
		border-radius: var(--radius-pill);
	}
	.fill.range {
		opacity: 0.4;
	}
	.tick {
		position: absolute;
		top: -3px;
		width: 3px;
		height: 0.75rem;
		margin-left: -1.5px;
		border-radius: 2px;
	}
	.deep {
		background: var(--stage-deep);
	}
	.light {
		background: var(--stage-light);
	}
	.rem {
		background: var(--stage-rem);
	}
	.awake {
		background: var(--stage-awake);
	}
	.other {
		background: var(--stage-other);
	}
	.grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	.grid > * {
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
	.avg {
		display: grid;
		gap: var(--space-3);
		margin: var(--space-4) 0 var(--space-3);
	}
	.avg .row {
		font-size: var(--text-sm);
	}
	.name {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
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
	.list li {
		border-top: 1px solid var(--color-border);
	}
	summary {
		display: grid;
		grid-template-columns: minmax(5.5rem, 7rem) minmax(0, 1fr) auto 1rem;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-3) 0;
		cursor: pointer;
		list-style: none;
	}
	summary::-webkit-details-marker {
		display: none;
	}
	summary::after {
		content: '';
		width: 0.4rem;
		height: 0.4rem;
		margin: 0 auto 0.2rem;
		border: solid var(--color-text-faint);
		border-width: 0 2px 2px 0;
		transform: rotate(45deg);
	}
	details[open] summary::after {
		margin-bottom: -0.2rem;
		transform: rotate(225deg);
	}
	summary:focus-visible {
		outline: none;
		box-shadow: var(--focus-ring);
		border-radius: var(--radius-sm);
	}
	.when,
	.mid {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		min-width: 0;
	}
	.when b {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.when span {
		font-size: var(--text-2xs);
	}
	.src {
		display: flex;
		align-items: center;
		gap: var(--space-1);
		font-size: var(--text-2xs);
		overflow-wrap: anywhere;
	}
	.dot {
		flex: none;
		width: 0.375rem;
		height: 0.375rem;
		background: var(--src, var(--color-neutral));
		border-radius: 50%;
	}
	.dur {
		font-size: var(--text-md);
		font-weight: 600;
		text-align: right;
	}
	.more {
		display: grid;
		justify-items: start;
		gap: var(--space-3);
		padding: 0 0 var(--space-3);
	}
	.members {
		display: grid;
		gap: var(--space-2);
		font-size: var(--text-xs);
	}
	.members li {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		border: 0;
	}
	.list > p {
		margin: var(--space-2) 0 0;
	}
	.members-grid {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(min(22rem, 100%), 1fr));
		align-items: start;
		gap: var(--space-3);
	}
	.member {
		display: grid;
		align-content: start;
		gap: var(--space-2);
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
		font-size: var(--text-sm);
	}
	.explain {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
	}
</style>
