<!--
	Connection overview: state and last outcome, manual sync (one job per stream, coalesced
	with a pending one) and reauthorization: the provider's OAuth page or the connector's prompts.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import HealthBadge from '../components/HealthBadge.svelte';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import AuthPrompt from './AuthPrompt.svelte';
	import { ago, goToProvider, providerLabel, safeHref, when, type Connection } from './connections.ts';

	let { connection, onchange }: { connection: Connection; onchange: (c: Connection) => void } = $props();

	let problem = $state<Problem | null>(null);
	let queued = $state<Schemas['Job'][] | null>(null);
	let prompt = $state<Schemas['AuthPromptStep'] | null>(null);
	let busy = $state(false);

	const syncs = $derived(connection.mode !== 'push');
	const name = $derived(providerLabel(connection.provider));

	async function sync() {
		busy = true;
		problem = null;
		queued = null;
		const { data, error } = await api.POST('/api/v1/connections/{id}/sync', { params: { path: { id: connection.id } } });
		busy = false;
		if (error) problem = error;
		else queued = data.jobs;
		const res = await api.GET('/api/v1/connections/{id}', { params: { path: { id: connection.id } } });
		if (res.data) onchange(res.data);
	}

	async function reauthorize() {
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/connections/{id}/auth/begin', { params: { path: { id: connection.id } } });
		if (error) {
			busy = false;
			problem = error;
			return;
		}
		if ('redirect_url' in data) {
			goToProvider(data.redirect_url);
		} else {
			busy = false;
			prompt = data;
		}
	}

	async function reauthorized() {
		prompt = null;
		const { data } = await api.GET('/api/v1/connections/{id}', { params: { path: { id: connection.id } } });
		if (data) onchange(data);
	}

	const streamOf = (j: Schemas['Job']) => (j.payload as { stream?: string })?.stream ?? j.kind;
</script>

{#if connection.health === 'needs_reauth'}
	<div class="callout" role="alert">
		<StatusIcon status="error" />
		<span>{name} no longer accepts the stored authorization. Sign in again to resume syncing; no data is lost.</span>
	</div>
{/if}

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
	<dd><code>{connection.id}</code></dd>
</dl>

<ProblemAlert {problem} />
{#if queued}
	<p class="done" role="status">
		<StatusIcon status="ok" />
		Sync queued: {queued.map((j) => `${streamOf(j)} (${j.status})`).join(', ') || 'nothing to sync'}.
		<a href="?tab=history">See history</a>
	</p>
{/if}

{#if syncs}
	<div class="actions">
		<button class="btn primary" type="button" disabled={busy || connection.status === 'paused'} onclick={sync}>Sync now</button>
		<button class={['btn', connection.health === 'needs_reauth' && 'primary']} type="button" disabled={busy} onclick={reauthorize}>
			Reauthorize
		</button>
		{#if connection.status === 'paused'}<span class="muted">Paused: resume it in Settings to sync.</span>{/if}
	</div>
{:else}
	<p class="muted">This source uploads its data to Vitamux; there is nothing to sync from here.</p>
{/if}

{#if prompt}
	<Modal title="Reauthorize {name}" onclose={() => (prompt = null)}>
		<AuthPrompt provider={connection.provider} step={prompt} onrestart={() => (prompt = null)} ondone={reauthorized} />
	</Modal>
{/if}

<style>
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-2) var(--space-5);
		margin: 0 0 var(--space-5);
	}
	dt {
		font-weight: 600;
	}
	dd {
		margin: 0;
	}
	.callout,
	.done {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin: 0 0 var(--space-4);
	}
	.callout {
		padding: var(--space-3);
		background: var(--color-error-bg);
		border: 1px solid var(--color-error);
		border-radius: var(--radius-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: center;
	}
</style>
