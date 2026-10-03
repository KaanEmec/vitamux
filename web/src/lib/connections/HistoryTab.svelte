<!-- Sync and backfill runs of a connection, newest first, one page at a time. -->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import { elapsed, when, type Connection } from './connections.ts';

	let { connection }: { connection: Connection } = $props();

	// Run outcomes recorded by internal/jobs/runner.go.
	const outcomes: Record<string, Status> = { succeeded: 'ok', failed: 'error', rescheduled: 'info', lease_expired: 'warn' };

	let runs = $state<Schemas['Run'][] | null>(null);
	let next = $state<string | undefined>(undefined);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);

	async function load(cursor?: string) {
		busy = true;
		const { data, error } = await api.GET('/api/v1/connections/{id}/runs', {
			params: { path: { id: connection.id }, query: { limit: 50, cursor } }
		});
		busy = false;
		problem = error ?? null;
		runs = [...(cursor ? (runs ?? []) : []), ...(data?.runs ?? [])];
		next = data?.has_more ? data.next_cursor : undefined;
	}

	$effect(() => {
		void load();
	});
</script>

<ProblemAlert {problem} />

{#if runs === null}
	<p class="muted" role="status">Loading history…</p>
{:else if runs.length}
	<table>
		<thead>
			<tr><th scope="col">Started</th><th scope="col">Run</th><th scope="col">Outcome</th><th scope="col">Took</th><th scope="col">Error</th></tr>
		</thead>
		<tbody>
			{#each runs as r (r.id)}
				<tr>
					<td>{when(r.started_at)}</td>
					<td><code>{r.kind}</code>{#if r.attempt > 1}<span class="muted"> · attempt {r.attempt}</span>{/if}</td>
					<td class="outcome"><StatusIcon status={r.outcome ? (outcomes[r.outcome] ?? 'info') : 'pending'} /> {r.outcome ?? 'running'}</td>
					<td>{elapsed(r.started_at, r.finished_at)}</td>
					<td>{#if r.error_class}<code>{r.error_class}</code>{/if} <span class="muted">{r.error_message ?? ''}</span></td>
				</tr>
			{/each}
		</tbody>
	</table>
	{#if next}
		<button class="btn more" type="button" disabled={busy} onclick={() => load(next)}>Load older runs</button>
	{/if}
{:else if !problem}
	<p class="muted">No runs yet.</p>
{/if}

<style>
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
	.outcome {
		white-space: nowrap;
	}
	.more {
		margin-top: var(--space-3);
	}
</style>
