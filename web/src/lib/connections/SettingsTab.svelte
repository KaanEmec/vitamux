<!--
	Connection settings: pause or resume, sync schedules (interval and on/off, saved on
	change), and removal with keep/delete data.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import DeleteDialog from './DeleteDialog.svelte';
	import { every, providerLabel, span, when, type Connection } from './connections.ts';

	type Schedule = Schemas['Schedule'];

	let { connection, onchange }: { connection: Connection; onchange: (c: Connection) => void } = $props();

	const intervals = [900, 1800, 3600, 3 * 3600, 6 * 3600, 12 * 3600, 86_400];

	let schedules = $state<Schedule[] | null>(null);
	let problem = $state<Problem | null>(null);
	let message = $state('');
	let busy = $state(false);
	let removing = $state(false);

	$effect(() => {
		void api.GET('/api/v1/schedules', { params: { query: { connection: connection.id } } }).then(({ data, error }) => {
			if (error) problem = error;
			schedules = data?.schedules ?? [];
		});
	});

	const canPause = $derived(connection.status === 'active' || connection.status === 'degraded');

	async function setPaused(paused: boolean) {
		busy = true;
		problem = null;
		message = '';
		const { data, error } = await api.PATCH('/api/v1/connections/{id}', {
			params: { path: { id: connection.id } },
			body: { status: paused ? 'paused' : 'active' }
		});
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		message = paused ? 'Syncing is paused.' : 'Syncing resumed.';
		onchange(data);
	}

	async function patch(s: Schedule, body: Schemas['SchedulePatch']) {
		problem = null;
		message = '';
		const { data, error } = await api.PATCH('/api/v1/schedules/{id}', { params: { path: { id: s.id } }, body });
		if (error) {
			problem = error;
			return;
		}
		schedules = schedules?.map((x) => (x.id === s.id ? data : x)) ?? null;
		message = `Schedule ${data.stream} (${data.mode}) saved.`;
	}

	function deleted(data: 'keep' | 'delete') {
		removing = false;
		if (data === 'delete') {
			void goto(`/connections?removed=${encodeURIComponent(connection.provider)}`);
			return;
		}
		void api.GET('/api/v1/connections/{id}', { params: { path: { id: connection.id } } }).then(({ data: c }) => {
			if (c) onchange(c);
		});
		message = 'Disconnected. The data stays; connect the same account again to resume.';
	}

	const options = (s: Schedule) => (intervals.includes(s.interval_seconds) ? intervals : [...intervals, s.interval_seconds].sort((a, b) => a - b));
</script>

<ProblemAlert {problem} />
{#if message}<p class="done" role="status"><StatusIcon status="ok" /> {message}</p>{/if}

<section aria-labelledby="sync-state">
	<h2 id="sync-state">Syncing</h2>
	{#if connection.status === 'paused'}
		<p>Scheduled and manual syncs are paused. Data already collected stays available.</p>
		<button class="btn primary" type="button" disabled={busy} onclick={() => setPaused(false)}>Resume syncing</button>
	{:else if canPause}
		<p>Pausing stops scheduled syncs until you resume; nothing is deleted.</p>
		<button class="btn" type="button" disabled={busy} onclick={() => setPaused(true)}>Pause syncing</button>
	{:else if connection.status === 'needs_reauth'}
		<p class="muted">Reauthorize this connection on the Overview tab to resume syncing.</p>
	{:else}
		<p class="muted">This connection is {connection.status}; connect the account again from Connections to resume.</p>
	{/if}
</section>

<section aria-labelledby="schedules">
	<h2 id="schedules">Schedules</h2>
	{#if schedules === null}
		<p class="muted" role="status">Loading schedules…</p>
	{:else if schedules.length}
		<table>
			<thead>
				<tr><th scope="col">Stream</th><th scope="col">Mode</th><th scope="col">Interval</th><th scope="col">Enabled</th><th scope="col">Next run</th></tr>
			</thead>
			<tbody>
				{#each schedules as s (s.id)}
					<tr>
						<th scope="row"><code>{s.stream}</code></th>
						<td>{s.mode}{#if s.lookback_seconds}<span class="muted"> · looks back {span(s.lookback_seconds)}</span>{/if}</td>
						<td>
							<select
								aria-label="Interval of {s.stream} {s.mode}"
								value={s.interval_seconds}
								onchange={(e) => patch(s, { interval_seconds: Number(e.currentTarget.value) })}
							>
								{#each options(s) as i (i)}<option value={i}>{every(i)}</option>{/each}
							</select>
						</td>
						<td>
							<input
								type="checkbox"
								aria-label="Enable {s.stream} {s.mode}"
								checked={s.enabled}
								onchange={(e) => patch(s, { enabled: e.currentTarget.checked })}
							/>
						</td>
						<td>{s.enabled ? when(s.next_run_at) : '–'}</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{:else}
		<p class="muted">No schedules.</p>
	{/if}
</section>

<section aria-labelledby="remove">
	<h2 id="remove">Remove</h2>
	<p>Disconnect {providerLabel(connection.provider)} and keep its data, or delete the connection with everything it collected.</p>
	<button class="btn" type="button" onclick={() => (removing = true)}>Remove connection…</button>
</section>

{#if removing}<DeleteDialog {connection} onclose={() => (removing = false)} ondeleted={deleted} />{/if}

<style>
	section {
		margin-bottom: var(--space-6);
	}
	.done {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-2);
		border-bottom: 1px solid var(--color-border);
		text-align: left;
	}
	select {
		font: inherit;
		padding: var(--space-1) var(--space-2);
	}
</style>
