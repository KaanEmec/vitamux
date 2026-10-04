<!--
	Connect a source (docs/adr/0021-source-setup.md, E20): every provider of GET /providers as a card
	with its setup state, and the one next action for the chosen one: the app wizard
	(needs_app_credentials), else POST /providers/{provider}/auth/begin, whose redirect leaves for the
	provider's OAuth page and whose prompts (sign-in, MFA code) AuthPrompt asks here. A provider
	whose install must change first cannot be chosen; its card says what to change (SidecarSteps,
	or the public URL problems). Used in the Connect a source dialog and inline on a first run.
	`start` preselects a provider, `app` opens its app wizard at step 1 (after a failed OAuth return).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import UnofficialBadge from '../components/UnofficialBadge.svelte';
	import AuthPrompt from '../connections/AuthPrompt.svelte';
	import Monogram from '../connections/Monogram.svelte';
	import { connectable, goToProvider } from '../connections/connections.ts';
	import { known, loadProviders } from '../connections/providers.svelte.ts';
	import Button from '../ui/Button.svelte';
	import AppWizard from './AppWizard.svelte';
	import SidecarSteps from './SidecarSteps.svelte';
	import { about, action, choosable, docsHref, explain, states } from './setup.ts';

	let { start }: { start?: { provider: string; app?: boolean } } = $props();

	let loaded = $state(false);
	let provider = $state('');
	let app = $state(false);
	let prompt = $state<Schemas['AuthPromptStep'] | null>(null);
	let problem = $state<Problem | null>(null);
	let lead = $state('');
	let busy = $state(false);
	const uid = $props.id();

	const providers = $derived(loaded ? connectable(known.list ?? []) : null);
	const chosen = $derived(providers?.find((p) => p.code === provider));

	onMount(async () => {
		problem = await loadProviders(true); // setup states change outside this page
		loaded = true;
		provider = start?.provider ?? providers?.find(choosable)?.code ?? '';
		app = !!(start?.app && chosen?.app_credentials);
	});

	async function begin() {
		if (!chosen) return;
		busy = true;
		problem = null;
		const { data, error, response } = await api.POST('/api/v1/providers/{provider}/auth/begin', { params: { path: { provider: chosen.code } } });
		if (error) {
			lead = await explain(error, response, chosen.code);
			problem = error;
			busy = false;
		} else if ('redirect_url' in data) {
			goToProvider(data.redirect_url);
		} else {
			busy = false;
			prompt = data;
		}
	}

	function next(e: SubmitEvent) {
		e.preventDefault();
		if (chosen?.setup_state === 'needs_app_credentials') app = true;
		else void begin();
	}

	function restart() {
		prompt = null;
		problem = null;
		app = false;
	}
</script>

{#if prompt && chosen}
	<AuthPrompt provider={chosen.code} step={prompt} onrestart={restart} />
{:else if providers === null}
	<p class="muted" role="status">Loading sources…</p>
{:else if app && chosen}
	<AppWizard provider={chosen} {busy} onback={restart} onconnect={begin} />
	<ProblemAlert {problem} {lead} />
{:else}
	<form onsubmit={next}>
		<fieldset>
			<legend class="visually-hidden">Sources</legend>
			{#each providers as p (p.code)}
				{@const open = choosable(p)}
				{@const st = states[p.setup_state]}
				<div class={['choice', !open && 'off']}>
					<label>
						<input type="radio" name="provider" value={p.code} bind:group={provider} disabled={!open} aria-describedby="{uid}-{p.code}" />
						<Monogram provider={p.code} />
						<span class="name">{p.name}</span>
						{#if !p.official && p.available}<UnofficialBadge />{/if}
					</label>
					<div class="about" id="{uid}-{p.code}">
						<p class="state">
							<StatusIcon status={st.status} />
							<span>{st.label}{#if p.upstream}<span class="muted">&nbsp;· {p.upstream.package} {p.upstream.version}</span>{/if}</span>
						</p>
						{#if about[p.code]}<p class="muted">{about[p.code]}</p>{/if}
						{#if !p.official && p.available}
							<p class="muted">It uses an unofficial API that can change without notice. The connection starts paused until you enable it on its page. Only connect your own account.</p>
						{/if}
						{#if p.setup_state === 'needs_public_url'}
							{#each p.problems as pr (pr.code)}<p>{pr.message}</p>{/each}
						{/if}
						{#if docsHref(p.code)}<p><a href={docsHref(p.code)} target="_blank" rel="noreferrer">About {p.name} in Vitamux</a></p>{/if}
					</div>
					{#if p.setup_state === 'needs_sidecar'}<SidecarSteps provider={p} />{/if}
				</div>
			{:else}
				<p class="muted">No source can be connected from here yet.</p>
			{/each}
		</fieldset>
		{#if chosen && choosable(chosen)}
			<p>
				{#if chosen.setup_state === 'needs_app_credentials'}
					{chosen.name} needs an application of your own at {chosen.name}: create it, paste its client id and secret here, then connect.
				{:else if chosen.auth_kind === 'interactive_mfa'}
					Vitamux asks for your {chosen.name} email and password here, then a verification code if {chosen.name} sends one. They pass to the sidecar once
					and are never stored; Vitamux keeps only the access it is given, encrypted.
				{:else}
					You will be sent to {chosen.name} to sign in and allow access, then come back here. Vitamux stores the access it is given encrypted and never shows it.
					{#if chosen.app_credentials?.managed_by_environment}It uses the app credentials set by the environment.{:else if chosen.app_credentials?.set}It uses the
						app credentials in <a href="/settings/sources">Settings, Sources</a>.{/if}
				{/if}
			</p>
		{/if}
		<p class="muted">Apple Health connects from the iPhone app (Settings, Devices); file imports use the command line.</p>
		<ProblemAlert {problem} {lead} />
		<Button variant="primary" type="submit" loading={busy} disabled={!chosen || !choosable(chosen)}>{chosen ? action(chosen) : 'Continue'}</Button>
	</form>
{/if}

<style>
	fieldset {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 18rem), 1fr));
		gap: var(--space-3);
		align-items: start;
		margin: 0 0 var(--space-4);
		padding: 0;
		border: 0;
	}
	.choice {
		padding: var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.choice:has(input:checked) {
		background: var(--color-accent-soft);
		border-color: var(--color-accent);
	}
	.choice:has(input:focus-visible) {
		outline: 2px solid var(--color-focus);
		outline-offset: 2px;
	}
	.choice label {
		display: flex;
		gap: var(--space-3);
		align-items: center;
		cursor: pointer;
	}
	.off label {
		cursor: not-allowed;
	}
	.off :global(.mono) {
		opacity: 0.6;
	}
	.name {
		flex: 1;
		font-weight: 600;
	}
	.about {
		display: grid;
		gap: var(--space-1);
		margin-top: var(--space-2);
		font-size: var(--text-sm);
	}
	.about p {
		margin: 0;
	}
	.state {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		font-weight: 500;
	}
	.state :global(.status-icon) {
		flex: none;
	}
</style>
