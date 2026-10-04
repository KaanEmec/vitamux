<!--
	Settings, Sources: the registered sidecars (GET /sidecars) and Add a sidecar (POST /sidecars with
	a name and a private-network URL). Vitamux generates the shared secret and returns it once: it is
	shown here until the owner dismisses it, never stored in the browser. Sidecars added here can be
	removed (DELETE; while connections of the provider exist the server answers 409 and a second
	confirmation removes it anyway); those of the environment are read-only.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fieldErrors, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import TextField from '../components/TextField.svelte';
	import Card from '../settings/Card.svelte';
	import Notice from '../settings/Notice.svelte';
	import { when } from '../settings/format.ts';
	import Button from '../ui/Button.svelte';
	import CopyValue from '../ui/CopyValue.svelte';

	type Sidecar = Schemas['Sidecar'];

	let sidecars = $state<Sidecar[] | null>(null);
	let problem = $state<Problem | null>(null);
	let lead = $state('');
	let removing = $state<string | null>(null);
	let force = $state(false);
	let removed = $state('');
	let busy = $state(false);

	let name = $state('');
	let url = $state('');
	let addProblem = $state<Problem | null>(null);
	let created = $state<{ name: string; secret: string } | null>(null);

	const errors = $derived(fieldErrors(addProblem));

	async function load() {
		const { data, error } = await api.GET('/api/v1/sidecars');
		if (error) problem = error;
		else sidecars = data.sidecars;
	}
	onMount(() => void load());

	async function add(e: SubmitEvent) {
		e.preventDefault();
		addProblem = null;
		created = null;
		busy = true;
		const { data, error } = await api.POST('/api/v1/sidecars', { body: { name: name.trim(), url: url.trim() } });
		busy = false;
		if (error) {
			addProblem = error;
			return;
		}
		created = { name: data.sidecar.name, secret: data.secret };
		name = '';
		url = '';
		await load();
	}

	async function remove(s: Sidecar) {
		const confirmed = force;
		problem = null;
		lead = '';
		removed = '';
		busy = true;
		const { error } = await api.DELETE('/api/v1/sidecars/{name}', { params: { path: { name: s.name }, query: confirmed ? { confirm: true } : {} } });
		busy = false;
		if (error?.status === 409 && !confirmed) {
			problem = error;
			lead = `Connections of ${s.name} still exist. Removing the sidecar anyway stops them syncing.`;
			force = true;
			return;
		}
		removing = null;
		force = false;
		if (error) {
			problem = error;
			return;
		}
		removed = s.name;
		await load();
	}
</script>

<Card title="Sidecars" id="sidecars" description="Connectors that run in their own container and talk to Vitamux over the private network.">
	{#if removed}<Notice>Removed the sidecar “{removed}”.</Notice>{/if}
	<ProblemAlert {problem} {lead} />
	{#if sidecars === null}
		{#if !problem}<p class="muted" role="status">Loading sidecars…</p>{/if}
	{:else if sidecars.length === 0}
		<p class="muted">No sidecar is registered.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">Sidecars</caption>
				<thead>
					<tr>
						<th scope="col">Name</th><th scope="col">Address</th><th scope="col">Status</th><th scope="col">Added</th>
						<th scope="col"><span class="visually-hidden">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each sidecars as s (s.name)}
						<tr>
							<th scope="row"><code>{s.name}</code></th>
							<td><code>{s.url}</code></td>
							<td>
								{#if s.available}<StatusIcon status="ok" /> Answering{:else}<StatusIcon status="off" /> Not answering{/if}
							</td>
							<td>{s.source === 'panel' ? when(s.created_at) : s.bundled ? 'Bundled' : 'Environment'}</td>
							<td>
								{#if s.source === 'environment'}
									<span class="muted">Read-only</span>
								{:else if removing === s.name}
									<div class="actions">
										<Button size="sm" variant="danger" disabled={busy} onclick={() => remove(s)}>{force ? 'Remove anyway' : 'Confirm remove'}</Button>
										<Button size="sm" onclick={() => ((removing = null), (force = false), (problem = null))}>Keep</Button>
									</div>
								{:else}
									<Button size="sm" onclick={() => ((removing = s.name), (force = false))} aria-label="Remove the sidecar {s.name}">Remove</Button>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</Card>

<Card title="Add a sidecar" id="add-sidecar" description="For a connector that is not bundled with Vitamux. Vitamux generates the shared secret the sidecar needs.">
	{#if created}
		<div class="callout" role="status">
			<strong>Sidecar “{created.name}” added.</strong>
			<p>Give this secret to the sidecar as its <code>VITAMUX_SIDECAR_SECRET_FILE</code>. It is shown once and cannot be retrieved later.</p>
			<CopyValue label="Shared secret" value={created.secret} />
			<p class="muted">Once the sidecar runs, use Check again under Connections, Connect a source.</p>
			<Button variant="primary" onclick={() => (created = null)}>I have saved it</Button>
		</div>
	{/if}
	<form onsubmit={add}>
		<TextField
			label="Name"
			name="name"
			bind:value={name}
			error={errors.name}
			hint="The provider code the sidecar describes, such as ultrahuman."
			pattern="[a-z][a-z0-9_]*"
			autocomplete="off"
			required
		/>
		<TextField
			label="Private address"
			name="url"
			type="url"
			bind:value={url}
			error={errors.url}
			hint="Its base URL on the private network, such as http://my-sidecar:8080. Public addresses are refused."
			autocomplete="off"
			required
		/>
		<ProblemAlert problem={addProblem} fields={['name', 'url']} />
		<Button variant="primary" type="submit" disabled={busy}>Add sidecar</Button>
	</form>
</Card>
