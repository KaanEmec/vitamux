<!--
	Settings, Sources: the owner's applications at providers (GET /providers `app_credentials`).
	Set or replace them (PUT, then verify) and remove them (DELETE; while connections use them the
	server answers 409 and a second confirmation removes them anyway). Values set by the environment
	are read-only. The secret is write-only: never shown, and cleared from the form once sent.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fieldErrors, type Problem } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import TextField from '../components/TextField.svelte';
	import { known, loadProviders } from '../connections/providers.svelte.ts';
	import Card from '../settings/Card.svelte';
	import Notice from '../settings/Notice.svelte';
	import { when } from '../settings/format.ts';
	import Button from '../ui/Button.svelte';
	import { updateProvider, type Provider } from './setup.ts';

	let loaded = $state(false);
	let problem = $state<Problem | null>(null);
	let lead = $state('');
	let notice = $state<{ status: Status; text: string } | null>(null);
	let editing = $state<Provider | null>(null);
	let removing = $state<string | null>(null);
	let force = $state(false);
	let busy = $state(false);
	let clientId = $state('');
	let secret = $state('');

	const apps = $derived((known.list ?? []).filter((p) => p.app_credentials));
	const errors = $derived(fieldErrors(problem));

	onMount(async () => {
		problem = await loadProviders(true);
		loaded = true;
	});

	function reset() {
		problem = null;
		lead = '';
		notice = null;
		removing = null;
		force = false;
	}

	function edit(p: Provider) {
		reset();
		editing = p;
		clientId = p.app_credentials?.client_id ?? '';
		secret = '';
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!editing) return;
		const p = editing;
		reset();
		busy = true;
		const body = { client_id: clientId.trim(), client_secret: secret.trim() };
		secret = '';
		const path = { params: { path: { provider: p.code } } };
		const put = await api.PUT('/api/v1/providers/{provider}/app-credentials', { ...path, body });
		if (put.error) {
			busy = false;
			problem = put.error;
			lead = put.error.status === 409 ? 'The environment sets them, so they cannot be changed here.' : '';
			return;
		}
		updateProvider(put.data);
		editing = null;
		const { data, error } = await api.POST('/api/v1/providers/{provider}/app-credentials/verify', path);
		busy = false;
		notice = data
			? { status: data.result === 'valid' ? 'ok' : data.result === 'invalid' ? 'error' : 'info', text: `Saved the ${p.name} app credentials. ${data.message}` }
			: { status: 'warn', text: `Saved the ${p.name} app credentials, but they could not be checked now (${error?.detail ?? 'no answer'}).` };
	}

	async function remove(p: Provider) {
		const confirmed = force;
		reset();
		busy = true;
		const { error } = await api.DELETE('/api/v1/providers/{provider}/app-credentials', {
			params: { path: { provider: p.code }, query: confirmed ? { confirm: true } : {} }
		});
		busy = false;
		if (error?.status === 409 && !confirmed) {
			problem = error;
			lead = 'Connections still use them. Removing them anyway stops those connections at their next token refresh, until new ones are set.';
			removing = p.code;
			force = true;
			return;
		}
		if (error) {
			problem = error;
			return;
		}
		await loadProviders(true);
		notice = { status: 'ok', text: `Removed the ${p.name} app credentials.` };
	}
</script>

<Card
	title="App credentials"
	id="app-credentials"
	description="Your own application at a provider, such as the Withings developer app. A value set by the environment wins and is shown read-only."
>
	{#if notice}<Notice status={notice.status}>{notice.text}</Notice>{/if}
	<ProblemAlert {problem} {lead} fields={editing ? ['client_id', 'client_secret'] : []} />

	{#if !loaded}
		<p class="muted" role="status">Loading…</p>
	{:else if apps.length === 0}
		<p class="muted">No connector here runs on an application of your own.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">App credentials</caption>
				<thead>
					<tr>
						<th scope="col">Provider</th><th scope="col">Client id</th><th scope="col">Status</th><th scope="col">Last changed</th>
						<th scope="col"><span class="visually-hidden">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each apps as p (p.code)}
						{@const a = p.app_credentials!}
						<tr>
							<th scope="row">{p.name}</th>
							<td>{#if a.client_id}<code>{a.client_id}</code>{:else}–{/if}</td>
							<td>
								{#if a.managed_by_environment}<StatusIcon status="info" /> Managed by the environment
								{:else if a.set}<StatusIcon status="ok" /> Set
								{:else}<StatusIcon status="off" /> Not set{/if}
							</td>
							<td>{when(a.updated_at)}</td>
							<td>
								{#if a.managed_by_environment}
									<span class="muted">Read-only</span>
								{:else if removing === p.code}
									<div class="actions">
										<Button size="sm" variant="danger" disabled={busy} onclick={() => remove(p)}>{force ? 'Remove anyway' : 'Confirm remove'}</Button>
										<Button size="sm" onclick={reset}>Keep</Button>
									</div>
								{:else}
									<div class="actions">
										<Button size="sm" onclick={() => edit(p)} aria-label="{a.set ? 'Replace' : 'Set'} the {p.name} app credentials">{a.set ? 'Replace' : 'Set'}</Button>
										{#if a.set}
											<Button size="sm" onclick={() => ((removing = p.code), (force = false))} aria-label="Remove the {p.name} app credentials">Remove</Button>
										{/if}
									</div>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}

	{#if editing}
		<form class="callout" onsubmit={save}>
			<p><strong>{editing.app_credentials?.set ? 'Replace' : 'Set'} the {editing.name} app credentials</strong></p>
			{#if editing.callback_url}<p class="muted">Its callback URL at {editing.name}: <code>{editing.callback_url}</code></p>{/if}
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
			<div class="actions">
				<Button variant="primary" type="submit" disabled={busy}>Save and check</Button>
				<Button onclick={() => (editing = null)}>Cancel</Button>
			</div>
		</form>
	{/if}
</Card>
