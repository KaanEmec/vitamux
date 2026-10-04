<!-- The shown night large: time asleep with its numbers and sources, the selected source's hypnogram and the time per stage. -->
<script lang="ts">
	import { stageColor, stageLabels } from '../charts/sleep.ts';
	import Chip from '../ui/Chip.svelte';
	import Skeleton from '../ui/Skeleton.svelte';
	import { hm, memberLabel } from './format.ts';
	import { clockText, episodeSpan, memberStages, nightLabel, nightSeconds, nightStages, selectedSessions, sessionsSpan, stageAxis, type Member, type Night, type Session } from './sleep.ts';

	let {
		night,
		last,
		sessions,
		timezone
	}: { night: Night; /** The latest night with data ("Last night"). */ last: boolean; /** With their stages; null while loading. */ sessions: Session[] | null; timezone?: string } = $props();

	const secs = $derived(nightSeconds(night));
	const stageSum = $derived(nightStages.reduce((n, s) => n + (secs[`sleep_${s}`] ?? 0), 0));
	const efficiency = $derived(secs.sleep_total && secs.sleep_in_bed ? Math.round((secs.sleep_total / secs.sleep_in_bed) * 100) : null);
	const span = $derived(episodeSpan(night, timezone));
	const picked = $derived(night.members.find((m) => m.selected));
	const axis = $derived(stageAxis(selectedSessions(night, new Map((sessions ?? []).map((s) => [s.id, s])))));
	/** "WHOOP · used" for the selected source, "Garmin · 7h 11m" for another with a total. */
	const chipText = (m: Member) => `${memberLabel(m)}${m.selected ? ' · used' : m.values?.sleep_total ? ` · ${hm(m.values.sleep_total)}` : ''}`;
</script>

<section class="card hero" aria-labelledby="hero-h">
	<div class="summary">
		<h2 id="hero-h" class="lbl">{last ? 'Last night' : 'Night'} · {nightLabel(night.local_date)}</h2>
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
		{#if sessions === null}
			<Skeleton variant="block" label="Loading sessions" />
		{:else if axis.staged.length}
			{#await import('../charts/Hypnogram.svelte') then { default: Hypnogram }}
				<Hypnogram
					stages={memberStages(axis.staged)}
					rows={axis.rows}
					from={axis.from}
					to={axis.to}
					rowHeight={36}
					{timezone}
					label="Sleep stages of {picked ? memberLabel(picked) : 'the night'}, {sessionsSpan(axis.staged)}"
				/>
			{/await}
		{:else}
			<p class="muted">No stage detail for this night.</p>
		{/if}
		<ul class="stages">
			{#each nightStages as st (st)}
				{@const v = secs[`sleep_${st}`]}
				<li>
					<span class="row"><span class="muted">{stageLabels[st]}</span><span class="num">{hm(v)}</span></span>
					<span class="track"><span class={['fill', stageColor(st)]} style:width="{stageSum && v ? (v / stageSum) * 100 : 0}%"></span></span>
				</li>
			{/each}
		</ul>
	</div>
</section>

<style>
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
	.lbl,
	dt {
		font-size: var(--text-2xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		color: var(--color-text-faint);
	}
	.lbl {
		margin: 0;
		font-weight: 500;
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
	.stages {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(7rem, 1fr));
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		list-style: none;
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
</style>
