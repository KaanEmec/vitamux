<!-- Every source that recorded a night, on one time axis, with its place in the rule, the naps of that day and the rule's explanation. -->
<script lang="ts">
	import type { Problem } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import ProvenanceDialog from '../components/ProvenanceDialog.svelte';
	import { clock, duration } from '../data/format.ts';
	import Badge from '../ui/Badge.svelte';
	import Chip from '../ui/Chip.svelte';
	import { dayLabel, hm, memberLabel, recordLabel, ruleTag } from './format.ts';
	import { memberSessions, memberStages, sessionsSpan, stageAxis, type Night, type Session } from './sleep.ts';

	let {
		night,
		sessions,
		problem,
		timezone
	}: { night: Night; /** With their stages; null while loading. */ sessions: Session[] | null; problem: Problem | null; timezone?: string } = $props();

	let provenanceId = $state<string | null>(null);

	const byId = $derived(new Map((sessions ?? []).map((s) => [s.id, s])));
	const axis = $derived(stageAxis(night.members.flatMap((m) => memberSessions(m, byId))));
	const naps = $derived(
		(sessions ?? []).filter((s) => s.is_nap && s.sleep_date === night.local_date && !night.members.some((m) => m.session_refs.includes(s.id)))
	);
</script>

<section class="card" aria-labelledby="across-h">
	<h2 id="across-h">Sources for the night of {dayLabel(night.local_date)}</h2>
	<p class="muted note">Every source that recorded it, on one time axis, and where each stands under the rule.</p>
	<ProblemAlert {problem} />
	<div class="members">
		{#each night.members as m, i (i)}
			{@const ss = memberSessions(m, byId)}
			{@const name = memberLabel(m)}
			<article class={['member', m.selected && 'selected']} aria-label={name}>
				<header>
					<Chip source={m.provider}>{name}</Chip>
					<Badge tone={m.selected ? 'accent' : 'neutral'}>{ruleTag(m)}</Badge>
				</header>
				{#if sessions && !m.selected}
					{#if ss.some((s) => s.stages?.length)}
						{#await import('../charts/Hypnogram.svelte') then { default: Hypnogram }}
							<Hypnogram stages={memberStages(ss)} rows={axis.rows} from={axis.from} to={axis.to} {timezone} label="Sleep stages of {name}, {sessionsSpan(ss)}" />
						{/await}
					{:else if ss.length}
						<p class="muted">This source reported no sleep stages.</p>
					{/if}
				{/if}
				<p class="muted meta">
					{#if ss.length}{sessionsSpan(ss)} ·{/if}
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
					{sessionsSpan([s])} · {duration((Date.parse(s.end_at) - Date.parse(s.start_at)) / 1000)}
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

{#if provenanceId}
	<ProvenanceDialog entity="sleep" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.members {
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
		margin: 0;
		padding: 0;
		font-size: var(--text-sm);
		list-style: none;
	}
	.explain {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
	}
</style>
