<!--
	The devices a connection's records were measured on (J20.7): type, name and records, saved on
	change. The type drives device-type rule groups (e.g. watch before phone for steps), so a type
	set here wins over the provider's. "Merge into…" moves every record of one device onto another
	of the same provider, for one physical device the provider reports twice; it cannot be undone.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import Notice from '../settings/Notice.svelte';
	import Button from '../ui/Button.svelte';
	import DataTable from './DataTable.svelte';
	import type { Connection } from './connections.ts';

	type Device = Schemas['SourceDevice'];
	type Records = Schemas['DeviceRecords'];

	let { connection }: { connection: Connection } = $props();

	let devices = $state<Device[] | null>(null);
	let types = $state<string[]>([]);
	let problem = $state<Problem | null>(null);
	let saved = $state('');
	let busy = $state(false);
	let merging = $state<Device | null>(null);
	let target = $state('');
	let confirmed = $state(false);

	async function load() {
		const { data, error } = await api.GET('/api/v1/source-devices', { params: { query: { include: ['records'] } } });
		if (error) problem = error;
		devices = data?.devices ?? [];
		types = data?.device_types ?? [];
	}

	$effect(() => {
		void load();
	});

	const own = (d: Device) => d.connections?.find((c) => c.connection_id === connection.id);
	// The provider's devices with records from this connection, or none anywhere yet.
	const listed = $derived(
		(devices ?? []).filter((d) => d.provider === connection.provider && !d.merged_into && (own(d) || !d.connections?.length))
	);
	const merged = $derived((devices ?? []).filter((d) => d.merged_into && listed.some((t) => t.id === d.merged_into)));

	const label = (d: Device) => d.name || d.model || d.fingerprint;
	const nf = new Intl.NumberFormat();
	const parts: [keyof Records, string, string][] = [
		['measurements', 'measurement', 'measurements'],
		['groups', 'reading', 'readings'],
		['sleep_sessions', 'sleep session', 'sleep sessions'],
		['workouts', 'workout', 'workouts'],
		['events', 'event', 'events']
	];
	function recordText(r: Records | undefined): string {
		const out = parts.filter(([k]) => r && r[k] > 0).map(([k, one, many]) => `${nf.format(r![k])} ${r![k] === 1 ? one : many}`);
		return out.length ? out.join(' · ') : 'No records';
	}
	/** Every record of the device, over all its connections: what a merge moves. */
	function total(d: Device): Records {
		const t: Records = { measurements: 0, groups: 0, sleep_sessions: 0, workouts: 0, events: 0 };
		for (const c of d.connections ?? []) for (const [k] of parts) t[k] += c.records[k];
		return t;
	}

	async function patch(d: Device, body: Schemas['SourceDevicePatch'], done: string) {
		problem = null;
		saved = '';
		busy = true;
		const { error } = await api.PATCH('/api/v1/source-devices/{id}', { params: { path: { id: d.id } }, body });
		busy = false;
		if (error) problem = error;
		else saved = done;
		await load();
	}

	function setType(d: Device, value: string) {
		void patch(d, { device_type: value || null }, `${label(d)} is ${value ? `a ${value.replaceAll('_', ' ')}` : 'untyped; the provider’s type applies'}. Resolved values are being recomputed.`);
	}

	function setName(d: Device, value: string) {
		const name = value.trim();
		if (name === (d.name ?? '')) return;
		void patch(d, { name: name || null }, name ? `Named ${name}.` : `Name of ${d.model || d.fingerprint} cleared.`);
	}

	function openMerge(d: Device) {
		merging = d;
		target = listed.find((x) => x.id !== d.id)?.id ?? '';
		confirmed = false;
	}

	async function merge(e: SubmitEvent) {
		e.preventDefault();
		if (!merging || !target) return;
		busy = true;
		problem = null;
		saved = '';
		const from = label(merging);
		const into = listed.find((x) => x.id === target);
		const { data, error } = await api.POST('/api/v1/source-devices/{id}/merge', {
			params: { path: { id: merging.id } },
			body: { into: target }
		});
		busy = false;
		merging = null;
		if (error) problem = error;
		else saved = `${from} merged into ${into ? label(into) : 'the other device'}: ${recordText(data.moved).toLowerCase()} moved. Resolved values are being recomputed.`;
		await load();
	}
</script>

<ProblemAlert {problem} />
{#if saved}<Notice>{saved}</Notice>{/if}

{#if devices === null}
	<p class="muted" role="status">Loading devices…</p>
{:else if listed.length}
	<DataTable label="Devices">
		<thead>
			<tr>
				<th scope="col">Device</th><th scope="col">Type</th><th scope="col">Name</th><th scope="col">Records</th>
				<th scope="col"><span class="visually-hidden">Actions</span></th>
			</tr>
		</thead>
		<tbody>
			{#each listed as d (d.id)}
				<tr>
					<th scope="row">
						{label(d)}
						<div class="muted id"><code>{d.fingerprint}</code>{#if d.manufacturer} · {d.manufacturer}{/if}</div>
					</th>
					<td>
						<select aria-label="Type of {label(d)}" value={d.device_type ?? ''} disabled={busy} onchange={(e) => setType(d, e.currentTarget.value)}>
							<option value="">Not set</option>
							{#each d.device_type && !types.includes(d.device_type) ? [...types, d.device_type] : types as t (t)}
								<option value={t}>{t.replaceAll('_', ' ')}</option>
							{/each}
						</select>
					</td>
					<td>
						<input
							aria-label="Name of {label(d)}"
							value={d.name ?? ''}
							placeholder={d.model || d.fingerprint}
							maxlength="100"
							disabled={busy}
							onchange={(e) => setName(d, e.currentTarget.value)}
						/>
					</td>
					<td>{recordText(own(d)?.records)}</td>
					<td>
						{#if listed.length > 1}
							<Button size="sm" disabled={busy} onclick={() => openMerge(d)}>Merge into…<span class="visually-hidden"> ({label(d)})</span></Button>
						{/if}
					</td>
				</tr>
			{/each}
		</tbody>
	</DataTable>
	{#if merged.length}
		<ul class="merged muted" aria-label="Merged devices">
			{#each merged as d (d.id)}
				<li><code>{d.fingerprint}</code> was merged into {label(listed.find((t) => t.id === d.merged_into)!)}; its new records are stored there.</li>
			{/each}
		</ul>
	{/if}
	<p class="muted hint">
		Rules select sources by device type, for example a watch before a phone for steps. Set the type when the provider does not say what a device is, and merge two entries of one physical device so all its data is on one device.
	</p>
{:else if !problem}
	<p class="muted">No devices yet. They appear once this connection has stored records.</p>
{/if}

{#if merging}
	{@const into = listed.find((x) => x.id === target)}
	<Modal title="Merge {label(merging)} into another device" onclose={() => (merging = null)}>
		<form onsubmit={merge}>
			<div class="field">
				<label for="merge-target">Merge into</label>
				<select id="merge-target" bind:value={target}>
					{#each listed.filter((x) => x.id !== merging!.id) as t (t.id)}<option value={t.id}>{label(t)} ({t.fingerprint})</option>{/each}
				</select>
			</div>
			<p>
				All records of {label(merging)} ({recordText(total(merging)).toLowerCase()}, older versions included) move to {into ? label(into) : 'the device you pick'},
				and records it reports later are stored there too. Its own type and name no longer apply. Rules that name {label(merging)} by its id stop matching it.
			</p>
			<label class="confirm">
				<input type="checkbox" bind:checked={confirmed} />
				<span>I understand that a merge cannot be undone here.</span>
			</label>
			<Button type="submit" variant="danger" disabled={busy || !confirmed || !target}>Merge devices</Button>
		</form>
	</Modal>
{/if}

<style>
	.id {
		font-size: var(--text-xs);
		font-weight: 400;
	}
	td select,
	td input {
		min-height: var(--control-h-sm);
		padding: 0 var(--space-2);
		font: inherit;
		color: var(--color-text);
		background: var(--color-inset);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-sm);
	}
	td input {
		width: 100%;
		min-width: 10rem;
	}
	.merged {
		margin: var(--space-3) 0 0;
		padding-left: var(--space-4);
		font-size: var(--text-sm);
	}
	.hint {
		margin: var(--space-3) 0 0;
		font-size: var(--text-sm);
	}
	.confirm {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		margin-bottom: var(--space-3);
	}
	.confirm input {
		margin-top: 0.3em;
	}
</style>
