<!--
	Connections: every source with its derived health, the connect wizard, and the outcome of
	an OAuth round trip (?connected=<provider> or ?auth_error=<code> from the callback).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem } from '#lib/api/client.ts';
	import HealthBadge from '#lib/components/HealthBadge.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import UnofficialBadge from '#lib/components/UnofficialBadge.svelte';
	import ConnectWizard from '#lib/connections/ConnectWizard.svelte';
	import { loadProviders } from '#lib/connections/providers.svelte.ts';
	import { ago, authErrors, providerLabel, type Connection } from '#lib/connections/connections.ts';

	const modes: Record<string, string> = { in_process: 'Server sync', push: 'Push uploads', remote: 'Sidecar' };

	let connections = $state<Connection[] | null>(null);
	let problem = $state<Problem | null>(null);
	let wizard = $state(false);

	const connected = $derived(page.url.searchParams.get('connected'));
	const authError = $derived(page.url.searchParams.get('auth_error'));
	const removed = $derived(page.url.searchParams.get('removed'));

	onMount(async () => {
		void loadProviders();
		const { data, error } = await api.GET('/api/v1/connections');
		problem = error ?? null;
		connections = data?.connections ?? [];
	});

	const dismiss = () => goto('/connections', { replace: true, reset: false });
</script>

<svelte:head><title>Connections · Vitamux</title></svelte:head>

<div class="head">
	<h1>Connections</h1>
	<button class="btn primary" type="button" onclick={() => (wizard = true)}>Connect a source</button>
</div>

{#if connected}
	<p class="banner" role="status">
		<StatusIcon status="ok" />
		<span>{providerLabel(connected)} is connected. A first sync has been queued.</span>
		<button class="btn link" type="button" onclick={dismiss}>Dismiss</button>
	</p>
{:else if authError}
	<div class="banner error" role="alert">
		<StatusIcon status="error" />
		<span>Connecting failed: {authErrors[authError] ?? `the provider answered ${authError}.`}</span>
		<button class="btn link" type="button" onclick={() => (wizard = true)}>Try again</button>
	</div>
{:else if removed}
	<p class="banner" role="status"><StatusIcon status="ok" /> <span>The {providerLabel(removed)} connection was removed.</span></p>
{/if}

<ProblemAlert {problem} />

{#if connections === null}
	<p class="muted" role="status">Loading connections…</p>
{:else if connections.length}
	<table>
		<thead>
			<tr><th scope="col">Source</th><th scope="col">Health</th><th scope="col">Last success</th><th scope="col">Type</th></tr>
		</thead>
		<tbody>
			{#each connections as c (c.id)}
				<tr>
					<th scope="row">
						<a href="/connections/{c.id}">{providerLabel(c.provider)}</a>
						{#if c.official === false}<UnofficialBadge />{/if}
					</th>
					<td>
						<HealthBadge health={c.health} />
						{#if c.health_reason}<div class="muted reason">{c.health_reason}</div>{/if}
					</td>
					<td>{ago(c.last_success_at)}</td>
					<td>{modes[c.mode] ?? c.mode}</td>
				</tr>
			{/each}
		</tbody>
	</table>
{:else if !problem}
	<p class="muted">No connections yet. Connect a source to start collecting data.</p>
{/if}

{#if wizard}<ConnectWizard onclose={() => (wizard = false)} />{/if}

<style>
	.head {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: baseline;
		justify-content: space-between;
	}
	.banner {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-3);
		margin: 0 0 var(--space-4);
		background: var(--color-surface);
		border: 1px solid var(--color-ok);
		border-radius: var(--radius-sm);
	}
	.banner.error {
		background: var(--color-error-bg);
		border-color: var(--color-error);
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
	tbody th {
		font-weight: 600;
	}
	.reason {
		font-size: var(--text-xs);
	}
</style>
