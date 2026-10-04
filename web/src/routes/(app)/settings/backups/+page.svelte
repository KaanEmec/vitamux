<!--
	Backups and export. The last backup comes from /system/status; backups themselves run
	from the CLI or a schedule (vitamux backup). An export is an async job: start it, poll
	its status, then download the zip through its one-time link.
-->
<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import Card from '#lib/settings/Card.svelte';
	import { ago, bytes, when } from '#lib/settings/format.ts';
	import { lastBackup, loadStatus } from '#lib/settings/status.ts';

	type Export = Schemas['Export'];

	let backupAt = $state<string | null>(null);
	let statusLoaded = $state(false);

	let format = $state<'ndjson' | 'csv'>('ndjson');
	let includeRaw = $state(false);
	let job = $state<Export | null>(null);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;
	const errors = $derived(fieldErrors(problem));

	onMount(async () => {
		const { status } = await loadStatus();
		backupAt = lastBackup(status);
		statusLoaded = true;
	});
	onDestroy(() => clearTimeout(timer));

	async function start(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		busy = true;
		const { data, error } = await api.POST('/api/v1/exports', { body: { format, include_raw: includeRaw } });
		if (error) {
			busy = false;
			problem = error;
			return;
		}
		job = data;
		poll(data.id);
	}

	function poll(id: string) {
		clearTimeout(timer);
		timer = setTimeout(async () => {
			const { data, error } = await api.GET('/api/v1/exports/{id}', { params: { path: { id } } });
			if (error) {
				busy = false;
				problem = error;
				return;
			}
			job = data;
			if (data.status === 'queued' || data.status === 'running') poll(id);
			else busy = false;
		}, 1000);
	}
</script>

<svelte:head><title>Backups and export · Vitamux</title></svelte:head>

<p class="lede">Backups restore this server. An export is a portable copy of your data.</p>

<Card
	title="Backups"
	id="backup"
	description="Backups are made with vitamux backup (see the backup and restore guide). An export below is a portable copy of your data, not a restorable backup."
>
	{#if !statusLoaded}
		<p class="muted" role="status">Checking the last backup…</p>
	{:else if backupAt}
		<p><StatusIcon status="ok" /> Last backup: <strong>{when(backupAt)}</strong> <span class="muted">({ago(backupAt)})</span></p>
	{:else}
		<p><StatusIcon status="warn" /> No backup is recorded.</p>
	{/if}
</Card>

<Card
	title="Export your data"
	id="export"
	description="The export is a zip with one file per table and a manifest. It holds all of your health rows, rules and audit history, so keep it private."
>
	<ProblemAlert {problem} fields={['format', 'include_raw']} />

	<form onsubmit={start}>
		<fieldset>
			<legend>Format</legend>
			<label class="check"><input type="radio" name="format" value="ndjson" bind:group={format} /> <span>NDJSON, one file per table</span></label>
			<label class="check"><input type="radio" name="format" value="csv" bind:group={format} /> <span>NDJSON plus <code>measurements.csv</code></span></label>
			{#if errors.format}<div class="field"><span class="error">{errors.format}</span></div>{/if}
		</fieldset>
		<label class="check">
			<input type="checkbox" name="include_raw" bind:checked={includeRaw} />
			<span>
				Include raw provider payloads
				<span class="hint">The original responses as received. Larger, and holds private health data as the providers sent it.</span>
			</span>
		</label>
		<button class="btn primary" type="submit" disabled={busy}>Start export</button>
	</form>

	{#if job}
		<div class="callout job" aria-live="polite">
			{#if job.status === 'queued' || job.status === 'running'}
				<p><StatusIcon status="pending" /> Export {job.status === 'queued' ? 'queued' : 'running'}…</p>
			{:else if job.status === 'failed'}
				<p><StatusIcon status="error" /> The export failed. Check the server log, then try again.</p>
			{:else}
				<p><StatusIcon status="ok" /> Export ready{job.size_bytes !== undefined ? ` (${bytes(job.size_bytes)})` : ''}.</p>
				{#if job.download_url}
					<p><a class="btn primary" href={job.download_url} download>Download export</a></p>
					<p class="muted">The link works once and expires 10 minutes after it was issued. Start a new export if it has expired.</p>
				{:else}
					<p class="muted">No download link is available. Start a new export.</p>
				{/if}
			{/if}
			<p class="muted">Started {when(job.created_at)}</p>
		</div>
	{/if}
</Card>

<style>
	.job {
		margin-top: var(--space-4);
	}
	.job p {
		margin: 0 0 var(--space-2);
	}
</style>
