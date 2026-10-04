<!--
	API keys: list, create (the token is shown once, in this page only) and revoke.
	Scopes follow the API contract: read:health, read:config, write:config, write:documents, admin.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { when } from '#lib/settings/format.ts';
	import Card from '#lib/settings/Card.svelte';
	import Notice from '#lib/settings/Notice.svelte';

	type Key = Schemas['APIKey'];
	type Scope = Schemas['Scope'];

	const scopes: { code: Scope; hint: string }[] = [
		{ code: 'read:health', hint: 'Read measurements, resolved values, sleep and workouts.' },
		{ code: 'read:config', hint: 'Read rules, settings, connections and status.' },
		{ code: 'write:config', hint: 'Change rules, settings, connections and overrides.' },
		{ code: 'write:documents', hint: 'Upload and review lab documents.' },
		{ code: 'admin', hint: 'Everything, including API keys and full exports.' }
	];

	let keys = $state<Key[] | null>(null);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);

	let name = $state('');
	let chosen = $state<Scope[]>(['read:health']);
	let expires = $state('');
	let created = $state<{ name: string; token: string } | null>(null);
	let revoking = $state<string | null>(null);
	let revoked = $state('');
	let copied = $state(false);

	const errors = $derived(fieldErrors(problem));

	async function load() {
		const { data, error } = await api.GET('/api/v1/api-keys');
		if (error) problem = error;
		else keys = data.api_keys;
	}
	onMount(() => void load());

	async function create(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		revoked = '';
		copied = false;
		busy = true;
		const body: { name: string; scopes: Scope[]; expires_at?: string } = {
			name: name.trim(),
			scopes: chosen
		};
		if (expires) body.expires_at = new Date(`${expires}T23:59:59`).toISOString();
		const { data, error } = await api.POST('/api/v1/api-keys', { body });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		created = { name: data.name, token: data.token };
		name = '';
		expires = '';
		chosen = ['read:health'];
		await load();
	}

	async function revoke(k: Key) {
		problem = null;
		busy = true;
		const { error } = await api.DELETE('/api/v1/api-keys/{id}', { params: { path: { id: k.id } } });
		busy = false;
		revoking = null;
		if (error) {
			problem = error;
			return;
		}
		revoked = k.name;
		await load();
	}

	async function copy() {
		try {
			await navigator.clipboard.writeText(created?.token ?? '');
			copied = true;
		} catch {
			copied = false;
		}
	}

	function keyState(k: Key): 'revoked' | 'expired' | 'active' {
		if (k.revoked_at) return 'revoked';
		if (k.expires_at && Date.parse(k.expires_at) <= Date.now()) return 'expired';
		return 'active';
	}
</script>

<svelte:head><title>API keys · Vitamux</title></svelte:head>

<p class="lede">Keys let scripts and other tools call the API. Each key has only the scopes you give it.</p>

<Card title="Your keys" id="keys">
	{#if created}
		<div class="inline-alert ok" role="status">
			<StatusIcon status="ok" />
			<div>
				<strong>Key "{created.name}" created.</strong>
				<p>Copy the secret now. It is shown once and cannot be retrieved later.</p>
				<code class="secret" aria-label="API key secret">{created.token}</code>
				<div class="actions">
					<button class="btn" type="button" onclick={copy}>{copied ? 'Copied' : 'Copy secret'}</button>
					<button class="btn primary" type="button" onclick={() => (created = null)}>I have saved it</button>
				</div>
			</div>
		</div>
	{/if}
	{#if revoked}<Notice>Revoked key "{revoked}".</Notice>{/if}
	<ProblemAlert {problem} fields={['name', 'scopes', 'expires_at']} />

	{#if keys === null}
		<p class="muted" role="status">Loading API keys…</p>
	{:else if keys.length === 0}
		<p class="muted">No API keys yet.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">API keys</caption>
				<thead>
					<tr>
						<th scope="col">Name</th><th scope="col">Scopes</th><th scope="col">Created</th><th scope="col">Last used</th>
						<th scope="col">Expires</th><th scope="col">Status</th><th scope="col"><span class="visually-hidden">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each keys as k (k.id)}
						{@const st = keyState(k)}
						<tr>
							<th scope="row">{k.name}</th>
							<td>{k.scopes.join(', ')}</td>
							<td>{when(k.created_at)}</td>
							<td>{k.last_used_at ? when(k.last_used_at) : 'Never'}</td>
							<td>{k.expires_at ? when(k.expires_at) : 'No expiry'}</td>
							<td>
								{#if st === 'active'}<StatusIcon status="ok" /> Active
								{:else if st === 'expired'}<StatusIcon status="warn" /> Expired
								{:else}<StatusIcon status="off" /> Revoked{/if}
							</td>
							<td>
								{#if st !== 'revoked'}
									{#if revoking === k.id}
										<div class="actions">
											<button class="btn sm danger primary" type="button" disabled={busy} onclick={() => revoke(k)}>Confirm revoke</button>
											<button class="btn sm" type="button" onclick={() => (revoking = null)}>Keep</button>
										</div>
									{:else}
										<button class="btn sm danger" type="button" onclick={() => (revoking = k.id)} aria-label="Revoke key {k.name}">Revoke</button>
									{/if}
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</Card>

<Card title="Create a key" id="new-key">
	<form onsubmit={create}>
		<TextField label="Name" name="name" bind:value={name} error={errors.name} hint="What the key is for, such as “home script”." maxlength={100} required />
		<fieldset>
			<legend>Scopes</legend>
			{#each scopes as s (s.code)}
				<label class="check">
					<input type="checkbox" name="scopes" value={s.code} bind:group={chosen} />
					<span><code>{s.code}</code><span class="hint">{s.hint}</span></span>
				</label>
			{/each}
			{#if errors.scopes}<div class="field"><span class="error">{errors.scopes}</span></div>{/if}
		</fieldset>
		<TextField label="Expires on (optional)" name="expires_at" bind:value={expires} error={errors.expires_at} type="date" />
		<button class="btn primary" type="submit" disabled={busy || chosen.length === 0}>Create key</button>
	</form>
</Card>
