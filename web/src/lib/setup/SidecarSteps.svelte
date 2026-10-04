<!--
	A sidecar provider that does not answer (needs_sidecar): what is wrong, the exact line that turns
	a bundled sidecar on for this install (Compose or Coolify, from GET /providers sidecar.enable)
	and Check again (POST /providers/{provider}/probe). Vitamux never starts containers itself.
-->
<script lang="ts">
	import { api, type Problem } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import Button from '../ui/Button.svelte';
	import CopyValue from '../ui/CopyValue.svelte';
	import { installs, updateProvider, type Provider } from './setup.ts';

	let { provider }: { provider: Provider } = $props();

	let busy = $state(false);
	let checked = $state('');
	let problem = $state<Problem | null>(null);

	async function probe() {
		busy = true;
		checked = '';
		problem = null;
		const { data, error } = await api.POST('/api/v1/providers/{provider}/probe', { params: { path: { provider: provider.code } } });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		updateProvider(data);
		checked = data.setup_state === 'needs_sidecar' ? `Checked: the ${provider.name} sidecar still does not answer.` : '';
	}
</script>

<div class="sidecar">
	{#each provider.problems as p (p.code)}<p>{p.message}</p>{/each}
	{#each provider.sidecar?.enable ?? [] as e (e.install)}
		<div class="install">
			<p class="install-name">{installs[e.install].name}</p>
			<CopyValue label={installs[e.install].where} value={e.line} />
			<p class="muted">
				Then {#if e.install === 'compose'}run <code>{e.apply}</code>{:else}{e.apply.charAt(0).toLowerCase() + e.apply.slice(1)}{/if}.
				{#if e.install === 'compose'}If .env already has a <code>COMPOSE_PROFILES</code> line, add <code>{e.line.split('=')[1]}</code> to it, separated by a comma.{/if}
			</p>
		</div>
	{/each}
	<div class="check">
		<Button size="sm" loading={busy} onclick={probe}>Check again</Button>
		<span class="muted" role="status">{checked}</span>
	</div>
	<ProblemAlert {problem} />
</div>

<style>
	.sidecar {
		display: grid;
		gap: var(--space-2);
		margin-top: var(--space-3);
		font-size: var(--text-sm);
	}
	p {
		margin: 0;
	}
	.install {
		padding: var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.install-name {
		margin-bottom: var(--space-2);
		font-weight: 600;
	}
	.check {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: center;
	}
</style>
