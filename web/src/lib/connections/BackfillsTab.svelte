<!--
	A connection's backfills, newest first, with unit progress. Start one (BackfillDialog),
	retry failed units (all, or one from the unit list) and cancel. Refreshes every few
	seconds while one runs.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import BackfillDialog from './BackfillDialog.svelte';
	import { day, when, type Connection } from './connections.ts';

	type Backfill = Schemas['Backfill'];

	let { connection }: { connection: Connection } = $props();

	const icons: Record<string, Status> = { running: 'pending', done: 'ok', failed: 'error', cancelled: 'off', pending: 'pending' };
	const pollMs = 5000;

	let backfills = $state<Backfill[] | null>(null);
	let streams = $state<string[]>([]);
	let problem = $state<Problem | null>(null);
	let dialog = $state(false);
	let busy = $state(false);
	let message = $state('');
	let open = $state<Backfill | null>(null);

	async function load() {
		const { data, error } = await api.GET('/api/v1/connections/{id}/backfills', { params: { path: { id: connection.id } } });
		if (error) problem = error;
		else backfills = data.backfills;
		if (open) await showUnits(open.id);
	}

	$effect(() => {
		void load();
		void api.GET('/api/v1/connections/{id}/streams', { params: { path: { id: connection.id } } }).then(({ data }) => {
			streams = data?.streams.map((s) => s.name) ?? [];
		});
	});

	const running = $derived(backfills?.some((b) => b.status === 'running') ?? false);
	$effect(() => {
		if (!running) return;
		const t = setInterval(load, pollMs);
		return () => clearInterval(t);
	});

	async function act(b: Backfill, action: 'retry' | 'cancel', unitStart?: string) {
		busy = true;
		problem = null;
		message = '';
		const params = { path: { id: connection.id, backfill_id: b.id } };
		const { data, error } =
			action === 'cancel'
				? await api.POST('/api/v1/connections/{id}/backfills/{backfill_id}/cancel', { params })
				: await api.POST('/api/v1/connections/{id}/backfills/{backfill_id}/retry', { params, body: unitStart ? { unit_start: unitStart } : {} });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		message = action === 'cancel' ? `Backfill of ${data.stream} cancelled; data fetched so far stays.` : `Failed units of ${data.stream} queued again.`;
		await load();
	}

	async function showUnits(id: string) {
		const { data, error } = await api.GET('/api/v1/connections/{id}/backfills/{backfill_id}', {
			params: { path: { id: connection.id, backfill_id: id } }
		});
		if (error) problem = error;
		else open = data;
	}

	function created(b: Backfill) {
		dialog = false;
		message = `Backfill of ${b.stream} started.`;
		void load();
	}

	const total = (b: Backfill) => b.unit_counts.pending + b.unit_counts.running + b.unit_counts.done + b.unit_counts.failed;
	const label = (b: Backfill) => `${b.stream} from ${day(b.start)}`;
</script>

<div class="bar">
	<button class="btn primary" type="button" disabled={connection.mode === 'push' || !streams.length} onclick={() => (dialog = true)}>
		New backfill
	</button>
	{#if connection.mode === 'push'}<span class="muted">Push sources upload their own history.</span>{/if}
</div>

<ProblemAlert {problem} />
{#if message}<p class="done" role="status"><StatusIcon status="ok" /> {message}</p>{/if}

{#if backfills === null}
	<p class="muted" role="status">Loading backfills…</p>
{:else if backfills.length}
	<table>
		<thead>
			<tr>
				<th scope="col">Stream</th><th scope="col">Range</th><th scope="col">Status</th><th scope="col">Units</th>
				<th scope="col">Started</th><th scope="col"><span class="visually-hidden">Actions</span></th>
			</tr>
		</thead>
		<tbody>
			{#each backfills as b (b.id)}
				<tr>
					<th scope="row"><code>{b.stream}</code></th>
					<td>{day(b.start)} – {day(b.end)}</td>
					<td class="nowrap"><StatusIcon status={icons[b.status] ?? 'info'} /> {b.status}</td>
					<td>
						<progress max={total(b) || 1} value={b.unit_counts.done} aria-label="Units done for {label(b)}"></progress>
						{b.unit_counts.done}/{total(b)} done{#if b.unit_counts.failed}, <strong>{b.unit_counts.failed} failed</strong>{/if}
					</td>
					<td>{when(b.created_at)}</td>
					<td class="actions">
						{#if b.unit_counts.failed && b.status !== 'cancelled'}
							<button class="btn" type="button" disabled={busy} onclick={() => act(b, 'retry')}>Retry failed<span class="visually-hidden"> units of {label(b)}</span></button>
						{/if}
						{#if b.status === 'running' || b.status === 'failed'}
							<button class="btn" type="button" disabled={busy} onclick={() => act(b, 'cancel')}>Cancel<span class="visually-hidden"> backfill of {label(b)}</span></button>
						{/if}
						<button class="btn link" type="button" aria-expanded={open?.id === b.id} onclick={() => (open?.id === b.id ? (open = null) : showUnits(b.id))}>
							Units<span class="visually-hidden"> of {label(b)}</span>
						</button>
					</td>
				</tr>
				{#if open?.id === b.id && open.units}
					<tr class="units">
						<td colspan="6">
							<table>
								<caption class="visually-hidden">Units of {label(b)}</caption>
								<thead><tr><th scope="col">Unit</th><th scope="col">Status</th><th scope="col">Attempts</th><th scope="col">Error</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr></thead>
								<tbody>
									{#each open.units as u (u.start)}
										<tr>
											<td>{day(u.start)} – {day(u.end)}</td>
											<td class="nowrap"><StatusIcon status={icons[u.status] ?? 'info'} /> {u.status}</td>
											<td>{u.attempts}</td>
											<td>{#if u.error_class}<code>{u.error_class}</code>{/if}</td>
											<td>
												{#if u.status === 'failed' && b.status !== 'cancelled'}
													<button class="btn" type="button" disabled={busy} onclick={() => act(b, 'retry', u.start)}>Retry<span class="visually-hidden"> unit from {day(u.start)}</span></button>
												{/if}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</td>
					</tr>
				{/if}
			{/each}
		</tbody>
	</table>
{:else if !problem}
	<p class="muted">No backfills yet. A backfill fetches history older than the regular sync window.</p>
{/if}

{#if dialog}
	<BackfillDialog {connection} {streams} onclose={() => (dialog = false)} oncreated={created} />
{/if}

<style>
	.bar,
	.done,
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.bar {
		margin-bottom: var(--space-4);
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
		vertical-align: top;
	}
	.units > td {
		padding-left: var(--space-6);
		background: var(--color-surface-2);
	}
	.nowrap {
		white-space: nowrap;
	}
	progress {
		width: 5rem;
		vertical-align: middle;
		accent-color: var(--color-accent);
	}
</style>
