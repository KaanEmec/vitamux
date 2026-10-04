<!--
	Connections: a card per source (health, last sync, 14-day run strip, fix-it action), the connect
	wizard, running backfills and the latest runs, and the outcome of an OAuth round trip
	(?connected=<provider> or ?auth_error=<code>&provider=<provider> from the callback). With no
	connection yet, the guided setup (Connect a source) is the page.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon, { type Status } from '#lib/components/StatusIcon.svelte';
	import ConnectionCard from '#lib/connections/ConnectionCard.svelte';
	import ConnectWizard from '#lib/connections/ConnectWizard.svelte';
	import DataTable from '#lib/connections/DataTable.svelte';
	import ProgressBar from '#lib/connections/ProgressBar.svelte';
	import { known, loadProviders } from '#lib/connections/providers.svelte.ts';
	import SourceSetup from '#lib/setup/SourceSetup.svelte';
	import Notice from '#lib/settings/Notice.svelte';
	import { alerting, authErrors, elapsed, providerLabel, when, type Connection } from '#lib/connections/connections.ts';
	import { loadRuns, type Run } from '#lib/connections/runs.ts';
	import Button from '#lib/ui/Button.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';

	type Backfill = Schemas['Backfill'];

	// Run outcomes recorded by internal/jobs/runner.go.
	const outcomes: Record<string, Status> = { succeeded: 'ok', failed: 'error', rescheduled: 'info', lease_expired: 'warn' };
	const plus = 'M12 5v14 M5 12h14';
	const latest = 8;

	let connections = $state<Connection[] | null>(null);
	let problem = $state<Problem | null>(null);
	let wizard = $state<{ provider: string; app?: boolean } | boolean>(false);
	let runs = $state<Record<string, Run[] | null>>({});
	let backfills = $state<Record<string, Backfill[]>>({});

	const connected = $derived(page.url.searchParams.get('connected'));
	const authError = $derived(page.url.searchParams.get('auth_error'));
	const failedProvider = $derived(page.url.searchParams.get('provider'));
	// A refused exchange with the owner's own app usually means a wrong secret or callback URL.
	const appSetup = $derived.by(() => {
		const app = known.list?.find((p) => p.code === failedProvider)?.app_credentials;
		return authError === 'exchange_failed' && !!app?.set && !app.managed_by_environment;
	});
	const removed = $derived(page.url.searchParams.get('removed'));

	const attention = $derived(connections?.filter((c) => alerting.includes(c.health)).length ?? 0);
	const healthy = $derived(connections?.filter((c) => c.health === 'ok').length ?? 0);
	const summary = $derived(
		connections?.length
			? `${connections.length} ${connections.length === 1 ? 'source' : 'sources'} · ${healthy} healthy${attention ? ` · ${attention} need${attention === 1 ? 's' : ''} attention` : ''}`
			: 'Sources send Vitamux your health data. Connect your first one below.'
	);
	const running = $derived(
		(connections ?? []).flatMap((c) => (backfills[c.id] ?? []).filter((b) => b.status === 'running').map((b) => ({ c, b })))
	);
	const recent = $derived(
		(connections ?? [])
			.flatMap((c) => (runs[c.id] ?? []).map((r) => ({ c, r })))
			.sort((a, b) => Date.parse(b.r.started_at) - Date.parse(a.r.started_at))
			.slice(0, latest)
	);

	async function loadRunsOf(c: Connection) {
		runs[c.id] = await loadRuns(c.id);
	}

	onMount(async () => {
		void loadProviders();
		const { data, error } = await api.GET('/api/v1/connections');
		problem = error ?? null;
		connections = data?.connections ?? [];
		for (const c of connections) {
			void loadRunsOf(c);
			if (c.mode === 'push') continue;
			void api.GET('/api/v1/connections/{id}/backfills', { params: { path: { id: c.id } } }).then(({ data }) => {
				if (data) backfills[c.id] = data.backfills;
			});
		}
	});

	function changed(c: Connection) {
		connections = connections?.map((x) => (x.id === c.id ? c : x)) ?? null;
		void loadRunsOf(c);
	}

	const dismiss = () => goto('/connections', { replace: true, reset: false });
	const total = (b: Backfill) => b.unit_counts.pending + b.unit_counts.running + b.unit_counts.done + b.unit_counts.failed;
</script>

<svelte:head><title>Connections · Vitamux</title></svelte:head>

<div class="head">
	<div>
		<h1>Connections</h1>
		<p class="muted summary">{summary}</p>
	</div>
	{#if connections?.length}<Button variant="primary" icon={plus} onclick={() => (wizard = true)}>Connect a source</Button>{/if}
</div>

{#if connected}
	<div class="inline-alert ok" role="status">
		<StatusIcon status="ok" />
		<span>{providerLabel(connected)} is connected. A first sync has been queued.</span>
		<div class="alert-actions"><Button variant="ghost" size="sm" onclick={dismiss}>Dismiss</Button></div>
	</div>
{:else if authError}
	<div class="inline-alert error" role="alert">
		<StatusIcon status="error" />
		<span>
			Connecting{failedProvider ? ` ${providerLabel(failedProvider)}` : ''} failed: {authErrors[authError] ?? `the provider answered ${authError}.`}
			{#if appSetup}Check the client id, the secret and the callback URL of your {providerLabel(failedProvider ?? '')} app.{/if}
		</span>
		<div class="alert-actions">
			<Button size="sm" onclick={() => (wizard = failedProvider ? { provider: failedProvider, app: appSetup } : true)}>
				{appSetup ? 'Review the app setup' : 'Try again'}
			</Button>
		</div>
	</div>
{:else if removed}
	<Notice>The {providerLabel(removed)} connection was removed.</Notice>
{/if}

<ProblemAlert {problem} />

{#if connections === null}
	<Skeleton variant="block" label="Loading connections" />
{:else if connections.length === 0}
	<section class="card" aria-labelledby="setup-title">
		<h2 id="setup-title">Connect a source</h2>
		<p class="muted lede">Pick a source. Each one shows what it needs, and every step happens here in the panel.</p>
		<SourceSetup />
	</section>
{:else}
	<div class="grid">
		{#each connections as c (c.id)}
			<ConnectionCard connection={c} runs={runs[c.id]} onchange={changed} />
		{/each}
		<button class="add" type="button" onclick={() => (wizard = true)}>
			<span class="plus"><Icon d={plus} size={20} /></span>
			<span class="title">Add a source</span>
			<span class="muted">Guided setup for Withings, Garmin, WHOOP, Apple Health or any push collector</span>
		</button>
	</div>

	<div class="panels">
		<section class="card backfills" aria-labelledby="backfills-title">
			<h2 id="backfills-title">Backfill in progress</h2>
			{#each running as { c, b } (b.id)}
				<div class="backfill">
					<div class="line">
						<span class="name"><a href="/connections/{c.id}?tab=backfills">{providerLabel(c.provider)}</a> · <code>{b.stream}</code></span>
						<span class="muted">{Math.round((b.unit_counts.done / (total(b) || 1)) * 100)}%</span>
					</div>
					<ProgressBar value={b.unit_counts.done} max={total(b)} label="Backfill of {b.stream} for {providerLabel(c.provider)}" />
					<div class="muted detail">{b.unit_counts.done} of {total(b)} units done{#if b.unit_counts.failed}, {b.unit_counts.failed} failed{/if} · resumes after restarts</div>
				</div>
			{:else}
				<p class="muted">No backfill is running. A backfill fetches history older than the regular sync window.</p>
			{/each}
		</section>

		<section class="runs" aria-labelledby="runs-title">
			<h2 id="runs-title">Recent sync runs</h2>
			{#if recent.length}
				<DataTable label="Recent sync runs">
					<thead>
						<tr><th scope="col">Started</th><th scope="col">Source</th><th scope="col">Run</th><th scope="col">Took</th><th scope="col">Result</th></tr>
					</thead>
					<tbody>
						{#each recent as { c, r } (r.id)}
							<tr>
								<td class="when">{when(r.started_at)}</td>
								<td><Chip source={c.provider}>{providerLabel(c.provider)}</Chip></td>
								<td><code>{r.kind}</code></td>
								<td>{elapsed(r.started_at, r.finished_at)}</td>
								<td>
									<span class="nowrap"><StatusIcon status={r.outcome ? (outcomes[r.outcome] ?? 'info') : 'pending'} /> {r.outcome ?? 'running'}</span>
									{#if r.error_class}<code class="muted">{r.error_class}</code>{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</DataTable>
			{:else}
				<p class="muted">No runs in the last 14 days.</p>
			{/if}
		</section>
	</div>
{/if}

{#if wizard}<ConnectWizard start={wizard === true ? undefined : wizard} onclose={() => (wizard = false)} />{/if}

<style>
	.head {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: flex-end;
		justify-content: space-between;
		margin-bottom: var(--space-5);
	}
	h1 {
		margin: 0;
	}
	.summary {
		margin: var(--space-1) 0 0;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
		gap: var(--space-4);
	}
	.add {
		display: flex;
		flex-direction: column;
		gap: var(--space-2);
		align-items: center;
		justify-content: center;
		min-height: 14rem;
		padding: var(--space-5);
		font: inherit;
		font-size: var(--text-sm);
		text-align: center;
		color: var(--color-text);
		background: transparent;
		border: 1.5px dashed var(--color-border-strong);
		border-radius: var(--radius-lg);
		cursor: pointer;
	}
	.add:hover {
		background: var(--color-surface);
	}
	.add .muted {
		max-width: 14rem;
	}
	.title {
		font-size: var(--text-md);
		font-weight: 600;
	}
	.plus {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 2.75rem;
		height: 2.75rem;
		color: var(--color-link);
		background: var(--color-surface-2);
		border-radius: var(--radius-md);
	}
	.panels {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		margin-top: var(--space-5);
	}
	.backfills {
		display: flex;
		flex: 1 1 20rem;
		flex-direction: column;
		gap: var(--space-3);
		align-self: flex-start;
	}
	.runs {
		flex: 2 1 32rem;
		min-width: 0;
	}
	h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	.lede {
		margin: var(--space-1) 0 var(--space-4);
	}
	.runs h2 {
		margin-bottom: var(--space-3);
	}
	.backfills p {
		margin: 0;
		font-size: var(--text-sm);
	}
	.backfill {
		display: grid;
		gap: var(--space-2);
	}
	.line {
		display: flex;
		gap: var(--space-3);
		justify-content: space-between;
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.detail {
		font-size: var(--text-xs);
	}
	.when,
	.nowrap {
		white-space: nowrap;
	}
</style>
