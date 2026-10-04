<!--
	A connection's actions: manual sync (one job per stream, coalesced with a pending one) and
	reauthorization (the provider's OAuth page or the connector's prompts). `compact` (list cards)
	shows only the fix-it action when the connection needs one: Reauthorize, or See streams
	for a degraded or failing one, beside Sync now; the detail header shows both.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import Button from '../ui/Button.svelte';
	import AuthPrompt from './AuthPrompt.svelte';
	import { goToProvider, providerLabel, type Connection } from './connections.ts';

	let { connection, compact = false, onchange }: { connection: Connection; compact?: boolean; onchange: (c: Connection) => void } = $props();

	let problem = $state<Problem | null>(null);
	let queued = $state<Schemas['Job'][] | null>(null);
	let prompt = $state<Schemas['AuthPromptStep'] | null>(null);
	let busy = $state(false);

	const reauth = $derived(connection.health === 'needs_reauth');
	const drifting = $derived(connection.health === 'degraded' || connection.health === 'failing');

	async function refresh() {
		const { data } = await api.GET('/api/v1/connections/{id}', { params: { path: { id: connection.id } } });
		if (data) onchange(data);
	}

	async function sync() {
		busy = true;
		problem = null;
		queued = null;
		const { data, error } = await api.POST('/api/v1/connections/{id}/sync', { params: { path: { id: connection.id } } });
		busy = false;
		if (error) problem = error;
		else queued = data.jobs;
		await refresh();
	}

	async function reauthorize() {
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/connections/{id}/auth/begin', { params: { path: { id: connection.id } } });
		if (error) {
			busy = false;
			problem = error;
		} else if ('redirect_url' in data) {
			goToProvider(data.redirect_url);
		} else {
			busy = false;
			prompt = data;
		}
	}

	async function reauthorized() {
		prompt = null;
		await refresh();
	}

	const streamOf = (j: Schemas['Job']) => (j.payload as { stream?: string })?.stream ?? j.kind;
</script>

{#if connection.mode === 'push'}
	{#if !compact}<p class="muted note">This source uploads its data to Vitamux; there is nothing to sync from here.</p>{/if}
{:else}
	<div class="actions">
		{#if !(compact && reauth)}
			<Button variant={!compact && !reauth ? 'primary' : 'secondary'} disabled={busy || connection.status === 'paused'} onclick={sync}>Sync now</Button>
		{/if}
		{#if !compact || reauth}
			<Button variant={reauth ? 'primary' : 'secondary'} disabled={busy} onclick={reauthorize}>Reauthorize</Button>
		{/if}
		{#if compact && drifting}<Button href="/connections/{connection.id}?tab=streams">See streams</Button>{/if}
	</div>
	{#if !compact && connection.status === 'paused'}<p class="muted note">Paused: resume it in Settings to sync.</p>{/if}
{/if}

<ProblemAlert {problem} />
{#if queued}
	<p class="done" role="status">
		<StatusIcon status="ok" />
		<span>Sync queued: {queued.map((j) => `${streamOf(j)} (${j.status})`).join(', ') || 'nothing to sync'}.</span>
		<a href="/connections/{connection.id}?tab=history">See history</a>
	</p>
{/if}

{#if prompt}
	<Modal title="Reauthorize {providerLabel(connection.provider)}" onclose={() => (prompt = null)}>
		<AuthPrompt provider={connection.provider} step={prompt} onrestart={() => (prompt = null)} ondone={reauthorized} />
	</Modal>
{/if}

<style>
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.note,
	.done {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
	}
	.done {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
</style>
