<!--
	One ECG recording (J22.18): the event as recorded (GET /events?code=ecg_recording, found by id)
	with Apple's classification word for word and its source, the average heart rate, symptoms and
	sampling details; then the waveform (GET /events/{id}/waveform) on paper at 25 mm/s and
	10 mm/mV, scrolled sideways, with a per-second table. A recording without a waveform (404) says
	so. No interpretation and no colouring by result.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { clock, formatValue } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayLabel, recordLabel } from '#lib/views/format.ts';
	import { layoutStrip, type Strip } from '#lib/watch/ecg.ts';
	import { classification, lead, num, symptoms, text } from '#lib/watch/watch.ts';

	type HealthEvent = Schemas['HealthEvent'];

	const id = $derived(page.params.id ?? '');

	let event = $state<HealthEvent | null | undefined>(undefined);
	let problem = $state<Problem | null>(null);
	let strip = $state<Strip | null | undefined>(undefined);
	let stripProblem = $state<Problem | null>(null);

	$effect(() => {
		const want = id;
		event = strip = undefined;
		problem = stripProblem = null;
		void readAll<HealthEvent>(async (cursor) => {
			const res = await api.GET('/api/v1/events', { params: { query: { code: ['ecg_recording'], limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.events, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (want !== id) return;
			event = res.items.find((e) => e.id === want) ?? null;
			problem = res.problem ?? null;
		});
		void api.GET('/api/v1/events/{id}/waveform', { params: { path: { id: want } } }).then(({ data, error }) => {
			if (want !== id) return;
			strip = data ? layoutStrip(data) : null;
			stripProblem = error && error.status !== 404 ? error : null;
		});
	});

	const facts = $derived.by(() => {
		if (!event) return [];
		const c = event.context;
		const hz = num(c, 'sampling_frequency_hz');
		const version = num(c, 'algorithm_version') ?? text(c, 'algorithm_version');
		return [
			['Recorded', `${dayLabel(event.local_date)} ${clock(event.start_at, event.tz_offset_min)}`],
			['Average heart rate', event.value == null ? 'Not recorded' : formatValue(event.value, 'bpm')],
			['Symptoms', symptoms(text(c, 'symptoms_status'))],
			...(hz == null ? [] : [['Sampling frequency', formatValue(hz, 'Hz')]]),
			...(text(c, 'lead') ? [['Lead', lead(text(c, 'lead'))]] : []),
			...(version == null ? [] : [['Algorithm version', String(version)]])
		];
	});
</script>

<svelte:head><title>ECG recording · Vitamux</title></svelte:head>

<nav class="crumbs" aria-label="Breadcrumb">
	<a href="/explore">Explore</a><span aria-hidden="true">/</span><a href="/explore/ecg">ECG</a><span aria-hidden="true">/</span><span aria-current="page">Recording</span>
</nav>
<h1>ECG recording</h1>

<ProblemAlert {problem} />

{#if event === undefined}
	<Skeleton variant="block" label="Loading the recording" />
{:else if event === null}
	{#if !problem}
		<EmptyState icon={icons.explore} title="No ECG recording with this id" text="The ECG list shows every recording Vitamux has stored.">
			<Button href="/explore/ecg">ECG recordings</Button>
		</EmptyState>
	{/if}
{:else}
	<section class="card" aria-labelledby="class-h">
		<p class="eyebrow muted">Classification recorded by Apple’s ECG app, shown as recorded</p>
		<h2 id="class-h" class="classification">{classification(event.level)}</h2>
		<p class="muted">{recordLabel(event.source)}</p>
		<dl>
			{#each facts as [term, value] (term)}
				<div><dt>{term}</dt><dd>{value}</dd></div>
			{/each}
		</dl>
	</section>

	<section class="card" aria-labelledby="wave-h">
		<h2 id="wave-h">Waveform</h2>
		<ProblemAlert problem={stripProblem} />
		{#if strip === undefined}
			<Skeleton variant="chart" label="Loading the waveform" />
		{:else if strip === null}
			{#if !stripProblem}<p class="muted">No waveform is stored for this recording.</p>{/if}
		{:else}
			{#await import('#lib/charts/EcgStrip.svelte') then { default: EcgStrip }}
				<EcgStrip {strip} label="ECG waveform at 25 mm/s and 10 mm/mV" />
			{/await}
			<p class="muted note">25 mm/s and 10 mm/mV; each small square is 0.04 s by 0.1 mV. {strip.summary}. Scroll sideways for the whole strip.</p>
		{/if}
	</section>
{/if}

<style>
	.crumbs {
		display: flex;
		gap: var(--space-2);
		margin-bottom: var(--space-3);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.crumbs a {
		text-decoration: none;
	}
	[aria-current] {
		color: var(--color-text);
	}
	h1 {
		margin: 0 0 var(--space-5);
		letter-spacing: var(--tracking-tight);
	}
	.card + .card {
		margin-top: var(--space-4);
	}
	.eyebrow {
		margin: 0;
		font-size: var(--text-sm);
	}
	.classification {
		margin: var(--space-1) 0;
		font-size: var(--text-xl);
	}
	p {
		margin: 0;
	}
	dl {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
		gap: var(--space-3) var(--space-5);
		margin: var(--space-4) 0 0;
	}
	dt {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	dd {
		margin: 0;
		font-variant-numeric: tabular-nums;
	}
	h2 {
		font-size: var(--text-lg);
	}
	.note {
		margin-top: var(--space-2);
		font-size: var(--text-sm);
	}
</style>
