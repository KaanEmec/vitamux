<!--
	The app wizard for a provider that runs on the owner's own application (Withings, J20.3):
	1. create the app at the provider with the values shown (callback URL first);
	2. paste the client id and secret (PUT …/app-credentials, then POST …/verify);
	3. connect the account (`onconnect`: the OAuth redirect).
	Readiness problems of the public URL come first. The secret is write-only: the field is cleared
	once sent and nothing returns it. Credentials set by the environment are shown read-only.
-->
<script lang="ts">
	import { api, fieldErrors, type Problem } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import TextField from '../components/TextField.svelte';
	import Button from '../ui/Button.svelte';
	import CopyValue from '../ui/CopyValue.svelte';
	import { appDashboards, updateProvider, type Provider } from './setup.ts';

	let {
		provider,
		busy = false,
		onback,
		onconnect
	}: { provider: Provider; busy?: boolean; onback: () => void; onconnect: () => void } = $props();

	const titles = ['Create the app', 'Paste the credentials', 'Connect your account'];
	let step = $state(1);
	let clientId = $state('');
	let secret = $state('');
	let saving = $state(false);
	let problem = $state<Problem | null>(null);
	let checked = $state<{ status: Status; text: string } | null>(null);

	const env = $derived(provider.app_credentials?.managed_by_environment ?? false);
	const dashboard = $derived(appDashboards[provider.code]);
	const errors = $derived(fieldErrors(problem));
	const headingId = $props.id();

	async function save(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		problem = null;
		checked = null;
		const body = { client_id: clientId.trim(), client_secret: secret.trim() };
		secret = '';
		const path = { params: { path: { provider: provider.code } } };
		const put = await api.PUT('/api/v1/providers/{provider}/app-credentials', { ...path, body });
		if (put.error) {
			saving = false;
			problem = put.error;
			return;
		}
		updateProvider(put.data);
		const { data, error } = await api.POST('/api/v1/providers/{provider}/app-credentials/verify', path);
		saving = false;
		if (data?.result === 'invalid') {
			checked = { status: 'error', text: data.message };
			return;
		}
		checked = data
			? { status: data.result === 'valid' ? 'ok' : 'info', text: data.message }
			: { status: 'warn', text: `They were saved but could not be checked now (${error?.detail ?? 'no answer'}). Connecting will check them.` };
		step = 3;
	}
</script>

<section aria-labelledby={headingId}>
	<ol class="progress" aria-label="Setup steps">
		{#each titles as t, i (t)}
			<li aria-current={step === i + 1 ? 'step' : undefined} class={[step > i + 1 && 'done']}>{t}</li>
		{/each}
	</ol>
	<h3 id={headingId}>{titles[step - 1]}</h3>

	{#if step === 1}
		{#each provider.problems as p (p.code)}
			<p class="note"><StatusIcon status="warn" /> <span>{p.message}</span></p>
		{/each}
		<p>
			{#if dashboard}Open the <a href={dashboard} target="_blank" rel="noreferrer">{provider.name} developer dashboard</a>, sign in and create an application.
			{:else}Create an application at {provider.name}.{/if}
			Enter these values there:
		</p>
		{#if provider.callback_url}
			<CopyValue label="Callback URL" value={provider.callback_url} />
		{:else}
			<p class="note"><StatusIcon status="warn" /> <span>No public address is set, so there is no callback URL yet. Set VITAMUX_PUBLIC_URL to the https address of Vitamux.</span></p>
		{/if}
		<CopyValue label="Application name" value="Vitamux" />
		<CopyValue label="Description" value="Self-hosted personal health data" />
		<p class="muted">
			The callback URL must match exactly. {provider.name} may keep the application in restricted mode, limited to 10 users: that is fine for one owner.
		</p>
		<div class="buttons">
			<Button onclick={onback}>Back</Button>
			<Button variant="primary" onclick={() => (step = 2)}>Next</Button>
		</div>
	{:else if step === 2}
		{#if env}
			<p>
				The client id and secret are set by the environment{#if provider.app_credentials?.client_id} (client id <code>{provider.app_credentials.client_id}</code>){/if},
				so they cannot be changed here.
			</p>
			<div class="buttons">
				<Button onclick={() => (step = 1)}>Back</Button>
				<Button variant="primary" onclick={() => (step = 3)}>Next</Button>
			</div>
		{:else}
			<form onsubmit={save}>
				<p>Copy them from the application you just created.</p>
				<TextField label="Client id" name="client_id" bind:value={clientId} error={errors.client_id} autocomplete="off" spellcheck={false} required />
				<TextField
					label="Client secret"
					name="client_secret"
					type="password"
					bind:value={secret}
					error={errors.client_secret}
					autocomplete="new-password"
					hint="Stored encrypted. Vitamux never shows it again."
					required
				/>
				{#if checked}<p class="note" role="alert"><StatusIcon status={checked.status} /> <span>{checked.text}</span></p>{/if}
				<ProblemAlert {problem} fields={['client_id', 'client_secret']} lead={problem?.status === 409 ? 'The environment sets them, so they cannot be changed here.' : ''} />
				<div class="buttons">
					<Button onclick={() => (step = 1)}>Back</Button>
					<Button variant="primary" type="submit" loading={saving}>Save and check</Button>
				</div>
			</form>
		{/if}
	{:else}
		{#if checked}<p class="note" role="status"><StatusIcon status={checked.status} /> <span>{checked.text}</span></p>{/if}
		<p>You will sign in at {provider.name} and allow access, then come back here. Vitamux stores the access it is given encrypted and never shows it.</p>
		<p class="muted">If {provider.name} reports a redirect URI error, the callback URL registered there differs from the one in step 1.</p>
		<div class="buttons">
			<Button onclick={() => (step = 2)}>Back</Button>
			<Button variant="primary" loading={busy} onclick={onconnect}>Continue to {provider.name}</Button>
		</div>
	{/if}
</section>

<style>
	.progress {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
		margin: 0 0 var(--space-4);
		padding: 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
		list-style: none;
		counter-reset: step;
	}
	.progress li {
		counter-increment: step;
	}
	.progress li::before {
		content: counter(step) '. ';
	}
	.progress [aria-current='step'] {
		font-weight: 600;
		color: var(--color-text);
	}
	.progress .done::before {
		content: '✓ ';
	}
	h3 {
		margin: 0 0 var(--space-3);
		font-size: var(--text-lg);
	}
	p {
		margin: 0 0 var(--space-3);
	}
	.note {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
	}
	.note :global(.status-icon) {
		flex: none;
		margin-top: 0.3em;
	}
	.buttons {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		justify-content: flex-end;
		margin-top: var(--space-4);
	}
</style>
