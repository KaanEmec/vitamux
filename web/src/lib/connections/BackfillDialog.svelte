<!--
	Start a backfill: one stream over a date range. The server splits the range into units of
	the connector's size, queues one job per unit and resumes failed units on retry.
-->
<script lang="ts">
	import { api, fieldErrors, type Problem, type Schemas } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import TextField from '../components/TextField.svelte';
	import { addDays, today } from '../data/format.ts';
	import Button from '../ui/Button.svelte';
	import { paced, providerLabel, unitDays, type Connection } from './connections.ts';

	let {
		connection,
		streams,
		onclose,
		oncreated
	}: { connection: Connection; streams: string[]; onclose: () => void; oncreated: (b: Schemas['Backfill']) => void } = $props();

	const now = today();
	const unit = $derived(unitDays(connection.provider));

	let stream = $state('');
	let start = $state(addDays(now, -365));
	let end = $state(now);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	const formId = $props.id();
	$effect.pre(() => {
		if (!stream && streams.length) stream = streams[0];
	});

	const errors = $derived(fieldErrors(problem));
	const localError = $derived(!start ? 'Choose a start date.' : end && end < start ? 'The end must be on or after the start.' : start > now ? 'The start cannot be in the future.' : '');
	const days = $derived(start && end && end >= start ? (Date.parse(end) - Date.parse(start)) / 86_400_000 + 1 : 0);

	const slow = $derived(paced[stream]);
	const plan = $derived(
		slow
			? `${days ? `${days} days, one per unit` : 'One day per unit'}, at most ${slow.perDay} a day${days ? ` (about ${Math.ceil(days / slow.perDay)} days to finish)` : ''}.`
			: (days ? `${days} days, fetched in ` : 'Fetched in ') +
					(unit ? `units of ${unit} days${days ? ` (about ${Math.ceil(days / unit)} units)` : ''}.` : 'units chosen by the connector.')
	);

	/** Local midnight of a YYYY-MM-DD date as RFC 3339. */
	const midnight = (d: string) => new Date(`${d}T00:00:00`).toISOString();

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		if (localError) return;
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/connections/{id}/backfills', {
			params: { path: { id: connection.id } },
			// The end is exclusive on the server; an end of today means "until now".
			body: { stream, start: midnight(start), end: end && end < now ? midnight(addDays(end, 1)) : undefined, daily_limit: slow?.perDay }
		});
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		oncreated(data);
	}
</script>

<Modal title="Backfill {providerLabel(connection.provider)}" {onclose}>
	<form id={formId} onsubmit={submit} novalidate>
		<div class="field">
			<label for="backfill-stream">Stream</label>
			<select id="backfill-stream" name="stream" bind:value={stream} required aria-invalid={errors.stream ? 'true' : undefined}>
				{#each streams as s (s)}<option value={s}>{s}</option>{/each}
			</select>
			{#if errors.stream}<span class="error">{errors.stream}</span>{/if}
		</div>
		<div class="range">
			<TextField label="From" name="start" type="date" max={now} bind:value={start} error={errors.start} required />
			<TextField label="To (inclusive)" name="end" type="date" max={now} bind:value={end} error={errors.end} hint="Today means until now." />
		</div>
		{#if slow}<p class="muted">{slow.note}</p>{/if}
		<p class="muted">{plan} Each unit runs as its own job and can be retried; data fetched before a cancel stays.</p>
		{#if localError}
			<div class="inline-alert error" role="alert"><StatusIcon status="error" /><span>{localError}</span></div>
		{/if}
		<ProblemAlert {problem} fields={['stream', 'start', 'end']} />
	</form>
	{#snippet footer()}
		<Button variant="primary" type="submit" form={formId} disabled={busy || !stream || !!localError}>Start backfill</Button>
	{/snippet}
</Modal>

<style>
	.range {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
		gap: 0 var(--space-4);
	}
</style>
