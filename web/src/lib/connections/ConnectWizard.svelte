<!--
	Connect wizard: pick a provider from GET /providers, then POST /providers/{provider}/auth/begin.
	A redirect step leaves for the provider's sign-in page, whose callback returns to
	/connections?connected=… or ?auth_error=… (docs/architecture/connectors.md#oauth-connection-flow);
	a prompt step is asked here by AuthPrompt. Unofficial connectors start paused.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import UnofficialBadge from '../components/UnofficialBadge.svelte';
	import AuthPrompt from './AuthPrompt.svelte';
	import { connectable, goToProvider } from './connections.ts';
	import { known, loadProviders } from './providers.svelte.ts';

	let { onclose }: { onclose: () => void } = $props();

	let loaded = $state(false);
	let provider = $state('');
	let prompt = $state<Schemas['AuthPromptStep'] | null>(null);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);

	const providers = $derived(loaded ? connectable(known.list ?? []) : null);
	const chosen = $derived(providers?.find((p) => p.code === provider));

	onMount(async () => {
		problem = await loadProviders(true); // availability of sidecars changes
		loaded = true;
		provider = providers?.find((p) => p.available)?.code ?? '';
	});

	async function begin(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/providers/{provider}/auth/begin', {
			params: { path: { provider } }
		});
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
</script>

<Modal title="Connect a source" {onclose}>
	{#if prompt && chosen}
		<AuthPrompt provider={chosen.code} step={prompt} onrestart={() => (prompt = null)} />
	{:else if providers === null}
		<p class="muted" role="status">Loading sources…</p>
	{:else}
		<form onsubmit={begin}>
			<fieldset>
				<legend>Provider</legend>
				{#each providers as p (p.code)}
					<div class="choice">
						<label>
							<input type="radio" name="provider" value={p.code} bind:group={provider} disabled={!p.available} aria-describedby={p.available && p.official ? undefined : `note-${p.code}`} />
							{p.name}
							{#if !p.official}<UnofficialBadge />{/if}
						</label>
						{#if !p.available}
							<div class="muted note" id="note-{p.code}">Not available: this source's connector is not running or has not answered yet.</div>
						{:else if !p.official}
							<div class="muted note" id="note-{p.code}">
								It uses an unofficial API that can change without notice. The connection starts paused until you enable it on its page.
							</div>
						{/if}
					</div>
				{:else}
					<p class="muted">No source can be connected from here yet.</p>
				{/each}
			</fieldset>
			{#if chosen}
				<p>
					{#if chosen.auth_kind === 'interactive_mfa'}
						Vitamux will ask for your sign-in details for {chosen.name} here and send them to the connector; it stores only the access it is given, encrypted, and never shows it.
					{:else}
						You will be sent to {chosen.name} to sign in and allow access, then come back here. Vitamux stores the access it is given encrypted and never shows it.
					{/if}
				</p>
			{/if}
			<p class="muted">Apple Health connects from the iPhone app (Settings, Devices); file imports use the command line.</p>
			<ProblemAlert {problem} />
			<button class="btn primary" type="submit" disabled={busy || !chosen}>
				{chosen ? `Continue to ${chosen.name}` : 'Continue'}
			</button>
		</form>
	{/if}
</Modal>

<style>
	fieldset {
		margin: 0 0 var(--space-4);
		padding: 0;
		border: 0;
	}
	legend {
		margin-bottom: var(--space-2);
		font-weight: 600;
		font-size: var(--text-sm);
	}
	.choice {
		padding: var(--space-2) 0;
	}
	.choice label {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	.note {
		padding-left: var(--space-5);
		font-size: var(--text-sm);
	}
</style>
