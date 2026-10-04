<!--
	Connection overview: a sync timeline (the 14-day run strip and the latest runs) and the
	connection's facts. Syncing and reauthorizing are in the page header (ConnectionActions).
-->
<script lang="ts">
	import HealthBadge from '../components/HealthBadge.svelte';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import RunStrip from './RunStrip.svelte';
	import { ago, elapsed, providerLabel, safeHref, when, type Connection } from './connections.ts';
	import { loadRuns, type Run } from './runs.ts';

	let { connection }: { connection: Connection } = $props();

	const outcomes: Record<string, Status> = { succeeded: 'ok', failed: 'error', rescheduled: 'info', lease_expired: 'warn' };
	const latest = 5;

	let runs = $state<Run[] | null | undefined>(undefined);

	// Reload when the connection changes (a manual sync updates last_success_at).
	$effect(() => {
		void connection.last_success_at;
		void loadRuns(connection.id).then((r) => (runs = r));
	});
</script>

{#if connection.health === 'needs_reauth'}
	<div class="inline-alert error" role="alert">
		<StatusIcon status="error" />
		<span>{providerLabel(connection.provider)} no longer accepts the stored authorization. Sign in again to resume syncing; no data is lost.</span>
	</div>
{/if}

<div class="cols">
	<section class="card timeline" aria-labelledby="timeline-title">
		<h2 id="timeline-title">Sync timeline</h2>
		<RunStrip {runs} large />
		{#if runs?.length}
			<ul class="runs" aria-label="Latest runs">
				{#each runs.slice(0, latest) as r (r.id)}
					<li>
						<StatusIcon status={r.outcome ? (outcomes[r.outcome] ?? 'info') : 'pending'} />
						<span class="kind"><code>{r.kind}</code> · {r.outcome ?? 'running'}</span>
						<span class="muted">{when(r.started_at)} · {elapsed(r.started_at, r.finished_at)}</span>
					</li>
				{/each}
			</ul>
			<a href="?tab=history">All runs</a>
		{:else if runs}
			<p class="muted">No runs in the last 14 days.</p>
		{/if}
	</section>

	<section class="card" aria-labelledby="facts-title">
		<h2 id="facts-title">Details</h2>
		<dl>
			<dt>Health</dt>
			<dd><HealthBadge health={connection.health} />{#if connection.health_reason}<span class="muted"> · {connection.health_reason}</span>{/if}</dd>
			<dt>Status</dt>
			<dd><code>{connection.status}</code></dd>
			<dt>API</dt>
			<dd>{connection.official === false ? 'Unofficial (may change without notice)' : connection.official ? 'Official' : 'Push uploads'}</dd>
			{#if connection.upstream}
				<dt>Upstream</dt>
				<dd>
					<a href={safeHref(connection.upstream.source_url)} rel="noreferrer noopener">{connection.upstream.package}</a>
					<code>{connection.upstream.version}</code>
				</dd>
			{/if}
			<dt>Last success</dt>
			<dd>{ago(connection.last_success_at)}<span class="muted">{connection.last_success_at ? ` · ${when(connection.last_success_at)}` : ''}</span></dd>
			<dt>Last error</dt>
			<dd>
				{#if connection.last_error_class}<code>{connection.last_error_class}</code>
					<span class="muted">· {connection.consecutive_failures} consecutive failures</span>{:else}None{/if}
			</dd>
			<dt>Connected</dt>
			<dd>{when(connection.created_at)}</dd>
			<dt>ID</dt>
			<dd><code class="id">{connection.id}</code></dd>
		</dl>
	</section>
</div>

<style>
	.cols {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: flex-start;
	}
	.cols > section {
		flex: 1 1 20rem;
		min-width: 0;
	}
	.timeline {
		flex-grow: 2;
	}
	h2 {
		margin: 0 0 var(--space-4);
		font-size: var(--text-md);
	}
	.runs {
		display: grid;
		gap: var(--space-2);
		margin: var(--space-4) 0;
		padding: 0;
		font-size: var(--text-sm);
		list-style: none;
	}
	.runs li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.kind {
		font-weight: 500;
	}
	dl {
		display: grid;
		grid-template-columns: max-content minmax(0, 1fr);
		gap: var(--space-2) var(--space-5);
		margin: 0;
		font-size: var(--text-sm);
	}
	dt {
		font-weight: 600;
	}
	dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	.id {
		font-size: var(--text-xs);
	}
</style>
