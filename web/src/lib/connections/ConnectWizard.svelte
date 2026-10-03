<!--
	Connect wizard: pick a provider, then POST /providers/{provider}/auth/begin and follow the
	redirect to the provider's sign-in page. The provider sends the browser back to
	/oauth/{provider}/callback, which redirects to /connections?connected=… or ?auth_error=…
	(docs/architecture/connectors.md#oauth-connection-flow).
-->
<script lang="ts">
	import { api, type Problem } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import UnofficialBadge from '../components/UnofficialBadge.svelte';
	import { connectable, goToProvider, providerLabel } from './connections.ts';

	let { onclose }: { onclose: () => void } = $props();

	let provider = $state(connectable[0]?.code ?? '');
	let problem = $state<Problem | null>(null);
	let busy = $state(false);

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
			return;
		}
		goToProvider(data.redirect_url);
	}
</script>

<Modal title="Connect a source" {onclose}>
	<form onsubmit={begin}>
		<fieldset>
			<legend>Provider</legend>
			{#each connectable as p (p.code)}
				<label class="choice">
					<input type="radio" name="provider" value={p.code} bind:group={provider} />
					{providerLabel(p.code)}
					{#if !p.official}<UnofficialBadge />{/if}
				</label>
			{/each}
		</fieldset>
		<p>
			You will be sent to {providerLabel(provider)} to sign in and allow access, then come back here. Vitamux
			stores the access it is given encrypted and never shows it.
		</p>
		<p class="muted">Apple Health connects from the iPhone app (Settings, Devices); file imports use the command line.</p>
		<ProblemAlert {problem} />
		<button class="btn primary" type="submit" disabled={busy || !provider}>Continue to {providerLabel(provider)}</button>
	</form>
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
		display: flex;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-2) 0;
	}
</style>
