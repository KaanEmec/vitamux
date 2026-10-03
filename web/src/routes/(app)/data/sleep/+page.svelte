<!--
	Sleep comparison: every session of one night (by the local wake date), one hypnogram per
	source on a shared time axis, with the totals each source reported. Query: ?date=.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import Hypnogram, { stageLabels, stageOrder } from '#lib/components/Hypnogram.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { addDays, clock, duration, isDate, today } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';

	type Session = Schemas['SleepSession'];

	let sessions = $state<Session[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(false);
	let provenanceId = $state<string | null>(null);

	const date = $derived.by(() => {
		const d = page.url.searchParams.get('date');
		return isDate(d) ? d : today();
	});
	let formDate = $derived(date);

	let generation = 0;
	$effect(() => {
		const d = date;
		const mine = ++generation;
		loading = true;
		readAll<Session>(async (cursor) => {
			const res = await api.GET('/api/v1/sleep', {
				params: { query: { start_date: d, end_date: d, include: ['stages'], limit: 100, cursor } }
			});
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.sleep, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((r) => {
			if (mine !== generation) return;
			loading = false;
			sessions = r.items;
			problem = r.problem ?? null;
		});
	});

	const from = $derived(Math.min(...sessions.map((s) => Date.parse(s.start_at))));
	const to = $derived(Math.max(...sessions.map((s) => Date.parse(s.end_at))));
	const rows = $derived(
		stageOrder.filter((st) => sessions.some((s) => s.stages?.some((x) => x.stage === st)))
	);
	const offset = $derived(sessions[0]?.tz_offset_min);

	function go(d: string) {
		void goto(`/data/sleep?date=${d}`);
	}

	function label(s: Session): string {
		const parts = [s.source.provider];
		const detail = s.source.origin ?? s.source.device_type;
		if (detail) parts.push(detail);
		return parts.join(' · ');
	}
	function totals(s: Session): { name: string; value: number | null }[] {
		return [
			{ name: 'Asleep', value: s.asleep_s },
			{ name: 'Deep', value: s.deep_s },
			{ name: 'Light', value: s.light_s },
			{ name: 'REM', value: s.rem_s },
			{ name: 'Awake', value: s.awake_s },
			{ name: 'Latency', value: s.latency_s }
		];
	}
</script>

<svelte:head><title>Sleep · Vitamux</title></svelte:head>

<h2>Sleep comparison</h2>
<p class="muted">Sessions that ended on the chosen local date, as each source reported them.</p>

<form
	class="controls"
	onsubmit={(e) => {
		e.preventDefault();
		if (isDate(formDate)) go(formDate);
	}}
>
	<button class="btn" type="button" onclick={() => go(addDays(date, -1))}>Previous night</button>
	<div class="field">
		<label for="date">Wake date</label>
		<input id="date" type="date" bind:value={formDate} required />
	</div>
	<button class="btn primary" type="submit">Show</button>
	<button class="btn" type="button" onclick={() => go(addDays(date, 1))}>Next night</button>
</form>

<ProblemAlert {problem} />

{#if loading}
	<p class="muted">Loading sessions…</p>
{:else if !sessions.length && !problem}
	<p class="muted">No sleep sessions on {date}.</p>
{:else if sessions.length}
	<div class="legend muted">
		Each bar sits on the row of its stage; all sessions share the time axis {clock(new Date(from).toISOString(), offset)} to {clock(new Date(to).toISOString(), offset)}.
	</div>
	{#each sessions as s (s.id)}
		<section class="card" aria-labelledby="s-{s.id}">
			<div class="head">
				<h3 id="s-{s.id}">{label(s)}{#if s.is_nap} <span class="muted">(nap)</span>{/if}</h3>
				<button class="btn" type="button" onclick={() => (provenanceId = s.id)}>
					Provenance<span class="visually-hidden"> of {label(s)}</span>
				</button>
			</div>
			<p class="muted small">
				{clock(s.start_at, s.tz_offset_min)} to {clock(s.end_at, s.tz_offset_min)} ·
				totals {s.totals_basis === 'provider' ? 'reported by the provider' : 'summed from stages'}
			</p>
			<dl class="totals">
				{#each totals(s) as t (t.name)}
					<div><dt>{t.name}</dt><dd>{duration(t.value)}</dd></div>
				{/each}
			</dl>
			{#if s.has_stages && s.stages?.length}
				<div class="hyp">
					<ul class="row-labels">
						{#each rows as r (r)}<li>{stageLabels[r]}</li>{/each}
					</ul>
					<Hypnogram
						stages={s.stages}
						{rows}
						{from}
						{to}
						label="Sleep stages of {label(s)}, {clock(s.start_at, s.tz_offset_min)} to {clock(s.end_at, s.tz_offset_min)}"
					/>
				</div>
			{:else}
				<p class="muted">This source reported no sleep stages.</p>
			{/if}
		</section>
	{/each}
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="sleep" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.controls {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: end;
		margin-bottom: var(--space-4);
	}
	.controls .field {
		margin-bottom: 0;
	}
	input[type='date'] {
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	section {
		margin-bottom: var(--space-4);
	}
	.head {
		display: flex;
		justify-content: space-between;
		gap: var(--space-3);
	}
	.small {
		font-size: var(--text-sm);
	}
	.totals {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-5);
		margin: 0 0 var(--space-3);
	}
	.totals dt {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.totals dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
	}
	.hyp {
		display: grid;
		grid-template-columns: 5rem 1fr;
		gap: var(--space-2);
		align-items: start;
	}
	.row-labels {
		display: grid;
		grid-auto-rows: 20px; /* matches rowHeight in Hypnogram.svelte */
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: var(--text-xs);
		line-height: 20px;
		color: var(--color-text-muted);
	}
	.legend {
		margin-bottom: var(--space-3);
		font-size: var(--text-sm);
	}
</style>
