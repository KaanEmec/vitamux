<!--
	Devices (J15.6): pair the Apple Health app with a QR code, see paired devices (last contact,
	requested types, "possibly denied" hints), open a device's source filter (J22.25, ./[id]/sources),
	ask one to resync, revoke it, and classify the apps (origins) data came from as native, relayed or direct.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import OriginTable from '#lib/devices/OriginTable.svelte';
	import PairingPanel from '#lib/devices/PairingPanel.svelte';
	import ResetDialog from '#lib/devices/ResetDialog.svelte';
	import { typeLabel } from '#lib/devices/format.ts';
	import { ago, when } from '#lib/settings/format.ts';
	import Card from '#lib/settings/Card.svelte';
	import Notice from '#lib/settings/Notice.svelte';

	type Device = Schemas['PairedDevice'];

	let devices = $state<Device[] | null>(null);
	let origins = $state<Schemas['DataOrigin'][] | null>(null);
	let targets = $state<Schemas['RelayTarget'][]>([]);
	let problem = $state<Problem | null>(null);
	let originsProblem = $state<Problem | null>(null);
	let busy = $state(false);
	let resetting = $state<Device | null>(null);
	let revoking = $state<string | null>(null);
	let notice = $state('');

	async function loadDevices() {
		const { data, error } = await api.GET('/api/v1/devices');
		if (error) problem = error;
		else devices = data.devices;
	}
	async function loadOrigins() {
		const { data, error } = await api.GET('/api/v1/origins');
		if (error) originsProblem = error;
		else {
			origins = data.origins;
			targets = data.relay_targets;
		}
	}
	onMount(() => void Promise.all([loadDevices(), loadOrigins()]));

	async function revoke(d: Device) {
		problem = null;
		notice = '';
		busy = true;
		const { error } = await api.POST('/api/v1/devices/{id}/revoke', { params: { path: { id: d.id } } });
		busy = false;
		revoking = null;
		if (error) {
			problem = error;
			return;
		}
		notice = `Revoked ${d.name}. Its next request is refused.`;
		await loadDevices();
	}

	const lastReset = (d: Device) => d.anchor_resets.map((r) => r.requested_at).sort().at(-1);
</script>

<svelte:head><title>Devices · Vitamux</title></svelte:head>

<p class="lede">Phones that upload Apple Health data, and how each origin app is treated in rules.</p>

<Card title="Pair a device" id="pair" description="Pair the Vitamux Apple Health app on an iPhone. The code works once and expires after 10 minutes.">
	<PairingPanel onexpired={loadDevices} />
</Card>

<Card title="Paired devices" id="paired">
	{#snippet aside()}
		<button class="btn sm" type="button" onclick={loadDevices}>Refresh</button>
	{/snippet}
	{#if notice}<Notice>{notice}</Notice>{/if}
	<ProblemAlert {problem} />
	{#if devices === null}
		<p class="muted" role="status">Loading devices…</p>
	{:else if devices.length === 0}
		<p class="muted">No devices paired yet.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">Paired devices</caption>
				<thead>
					<tr>
						<th scope="col">Device</th><th scope="col">Last seen</th><th scope="col">Last sync</th>
						<th scope="col">Requested types</th><th scope="col"><span class="visually-hidden">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each devices as d (d.id)}
						<tr>
							<th scope="row">
								{d.name}
								{#if d.revoked_at}<br /><StatusIcon status="off" /> Revoked {when(d.revoked_at)}{/if}
							</th>
							<td>{d.last_seen_at ? `${when(d.last_seen_at)} (${ago(d.last_seen_at)})` : 'Never'}</td>
							<td>{d.last_sync_at ? `${when(d.last_sync_at)} (${ago(d.last_sync_at)})` : 'Never'}</td>
							<td>
								{#if d.types.length === 0}
									<span class="muted">Not reported yet</span>
								{:else}
									<ul class="types">
										{#each d.types as t (t)}
											<li title={t}>
												{typeLabel(t)}
												{#if d.possibly_denied.includes(t)}<span class="flag"><StatusIcon status="warn" /> possibly denied</span>{/if}
											</li>
										{/each}
									</ul>
								{/if}
								{#if lastReset(d)}<p class="muted">Resync requested {when(lastReset(d))}.</p>{/if}
							</td>
							<td>
								{#if !d.revoked_at}
									{#if revoking === d.id}
										<div class="actions">
											<button class="btn sm destructive primary" type="button" disabled={busy} onclick={() => revoke(d)}>Confirm revoke</button>
											<button class="btn sm" type="button" onclick={() => (revoking = null)}>Keep</button>
										</div>
									{:else}
										<div class="actions">
											<a class="btn sm" href="/settings/devices/{d.id}/sources" aria-label="Sources of {d.name}">Sources</a>
											<button class="btn sm" type="button" onclick={() => (resetting = d)} aria-label="Resync {d.name}">Resync…</button>
											<button class="btn sm destructive" type="button" onclick={() => (revoking = d.id)} aria-label="Revoke {d.name}">Revoke</button>
										</div>
									{/if}
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<p class="muted note">
			iOS does not tell Vitamux when you deny a type. A requested type that stored nothing for 7 days is flagged as possibly denied;
			check Settings › Health › Data Access on the phone. It may also just have nothing new.
		</p>
	{/if}
</Card>

<Card
	title="Origin apps in Apple Health"
	id="origins"
	description="The apps that recorded your data inside Apple Health. Mark an app that relays another vendor's data (such as Garmin Connect), so rules can prefer direct data or leave the relayed copy out."
>
	<ProblemAlert problem={originsProblem} />
	{#if origins === null}
		<p class="muted" role="status">Loading origins…</p>
	{:else}
		<OriginTable {origins} {targets} onchanged={loadOrigins} />
	{/if}
</Card>

{#if resetting}
	<ResetDialog
		device={resetting}
		onclose={() => (resetting = null)}
		ondone={(what) => {
			notice = `Resync requested for ${what}. The device starts it on its next contact.`;
			void loadDevices();
		}}
	/>
{/if}

<style>
	.types {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		padding: 0;
		margin: 0;
		list-style: none;
	}
	.flag {
		color: var(--color-text-muted);
		font-size: var(--text-sm);
	}
	.note {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
	}
</style>
