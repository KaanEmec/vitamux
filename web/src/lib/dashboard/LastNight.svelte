<!--
	Last night: the resolved night of the day shown (GET /resolved/sleep), its time asleep, bed and
	wake time (the main episode), the selected source, the hypnogram of that source's sessions
	(GET /sleep with stages) and the time in each stage. Nothing renders without a night.
-->
<script lang="ts">
	import { api, type Schemas } from '../api/client.ts';
	import { formatClock } from '../charts/scale.ts';
	import { addDays } from '../data/format.ts';
	import Chip from '../ui/Chip.svelte';
	import MetricTile from '../ui/MetricTile.svelte';
	import { hm, providerName } from '../views/format.ts';
	import { memberStages, nightSeconds, selectedSessions, stageAxis, stageSeconds, type Night, type Session } from '../views/sleep.ts';

	let { day, mean }: { day: string; /** The 30-night mean of time asleep, in seconds. */ mean?: number } = $props();

	let night = $state<Night | null>(null);
	let timezone = $state<string | undefined>();
	let sessions = $state<Session[]>([]);

	const stack = import('../charts/StageStack.svelte');
	const hypnogram = import('../charts/Hypnogram.svelte');

	$effect(() => {
		const d = day;
		let stale = false;
		night = null;
		sessions = [];
		void api.GET('/api/v1/resolved/sleep', { params: { query: { start_date: d, end_date: d } } }).then(async ({ data }) => {
			const n = data?.nights.find((x) => x.local_date === d && x.result.status !== 'no_data');
			if (stale || !n) return;
			night = n;
			timezone = data?.timezone || undefined;
			// A night's sessions can be dated the day before (ADR-0009).
			const res = await api.GET('/api/v1/sleep', { params: { query: { start_date: addDays(d, -1), end_date: d, include: ['stages'], limit: 50 } } });
			if (!stale) sessions = res.data?.sleep ?? [];
		});
		return () => (stale = true);
	});

	const seconds = $derived(night ? nightSeconds(night) : {});
	const source = $derived(night?.members.find((m) => m.selected)?.provider);
	const axis = $derived(stageAxis(night ? selectedSessions(night, new Map(sessions.map((s) => [s.id, s]))) : []));
	const stages = $derived(night ? stageSeconds(night) : []);
	const clock = (iso: string) => formatClock(Date.parse(iso), timezone);
	const span = (e: Schemas['Span']) => `${clock(e.start)} → ${clock(e.end)}`;
</script>

{#if night && seconds.sleep_total != null}
	<section class="card night" aria-labelledby="night-title">
		<div class="head">
			<MetricTile code="sleep" />
			<h2 id="night-title">Last night</h2>
			{#if source}<Chip {source}>{providerName(source)}</Chip>{/if}
		</div>
		<p class="total">
			{hm(seconds.sleep_total)}<span class="visually-hidden"> asleep</span>
			{#if night.episode}<span class="span"><span class="visually-hidden">In bed </span>{span(night.episode)}</span>{/if}
		</p>
		{#if axis.staged.length}
			{#await hypnogram then { default: Hypnogram }}
				<Hypnogram stages={memberStages(axis.staged)} rows={axis.rows} from={axis.from} to={axis.to} label="Sleep stages of last night" {timezone} />
			{/await}
		{/if}
		{#if stages.some((s) => s.seconds > 0)}
			{#await stack then { default: StageStack }}
				<StageStack {stages} label="Time in each sleep stage last night" />
			{/await}
		{/if}
		<footer>
			{#if mean != null}<span>30-night mean {hm(mean)}</span>{/if}
			<a href="/explore/sleep">Sleep view →</a>
		</footer>
	</section>
{/if}

<style>
	.night {
		display: flex;
		flex: 1 1 20rem;
		flex-direction: column;
		gap: var(--space-4);
		min-width: 0;
		--metric: var(--metric-sleep);
	}
	.head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
	}
	h2 {
		flex: 1;
		margin: 0;
		font-size: var(--text-md);
	}
	.total {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-3);
		margin: 0;
		font-size: var(--text-display);
		font-weight: 600;
		letter-spacing: var(--tracking-tight);
		font-variant-numeric: tabular-nums;
	}
	.span {
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: normal;
		color: var(--color-text-muted);
	}
	footer {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		margin-top: auto;
		padding-top: var(--space-3);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
		border-top: 1px solid var(--color-border);
	}
	footer a {
		margin-left: auto;
		font-size: var(--text-sm);
		font-weight: 500;
		text-decoration: none;
	}
</style>
