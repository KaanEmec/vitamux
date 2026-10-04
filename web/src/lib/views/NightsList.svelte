<!-- The nights of a period, newest first: bed and wake time, stages, sources and time asleep; each opens to its sources and "Show this night". -->
<script lang="ts">
	import StageStack from '../charts/StageStack.svelte';
	import Chip from '../ui/Chip.svelte';
	import { sourceClass } from '../ui/source.ts';
	import { dayLabel, hm, memberLabel, ruleTag } from './format.ts';
	import { clockText, episodeSpan, nightSeconds, shortDay, stageSeconds, type Night } from './sleep.ts';

	let { nights, timezone, onshow }: { /** Newest first. */ nights: Night[]; timezone?: string; onshow: (n: Night) => void } = $props();

	const pageSize = 7;
	// Back to one page whenever the nights change.
	let shown = $derived((void nights, pageSize));

	const sourceText = (n: Night) => {
		const used = n.members.find((m) => m.selected);
		const rest = n.members.filter((m) => !m.selected).map(memberLabel);
		return `${used ? memberLabel(used) : 'No source in the rule'}${rest.length ? ` · ${rest.join(', ')} also recorded` : ''}`;
	};
	const bedWake = (n: Night) => {
		const s = episodeSpan(n, timezone);
		return s ? `${clockText(s.bed)} → ${clockText(s.wake)}` : '';
	};
</script>

<section class="card list" aria-labelledby="nights-h">
	<h2 id="nights-h">Nights</h2>
	<ul>
		{#each nights.slice(0, shown) as n (n.local_date)}
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
						<button class="btn sm" type="button" onclick={() => onshow(n)}>Show this night<span class="visually-hidden"> of {dayLabel(n.local_date)}</span></button>
					</div>
				</details>
			</li>
		{/each}
	</ul>
	{#if nights.length > shown}
		<p><button class="btn sm" type="button" onclick={() => (shown += pageSize)}>Show more ({nights.length - shown} left)</button></p>
	{/if}
</section>

<style>
	h2 {
		margin-bottom: var(--space-1);
	}
	ul {
		margin: 0;
		padding: 0;
		list-style: none;
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
</style>
