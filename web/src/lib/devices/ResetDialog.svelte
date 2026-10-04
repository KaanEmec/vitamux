<!--
	Asks a device to resync from scratch on its next contact: every type, or only the chosen ones.
	Re-sent samples are deduplicated by UUID, so a reset only costs time (apple-health.md#sync-algorithm).
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import Modal from '#lib/components/Modal.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { typeLabel } from './format.ts';

	let { device, onclose, ondone }: { device: Schemas['PairedDevice']; onclose: () => void; ondone: (what: string) => void } = $props();

	let scope = $state<'all' | 'some'>('all');
	let chosen = $state<string[]>([]);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	const formId = $props.id();

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		busy = true;
		const all = scope === 'all';
		const { error } = await api.POST('/api/v1/devices/{id}/request-anchor-reset', {
			params: { path: { id: device.id } },
			body: all ? {} : { types: chosen }
		});
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		ondone(all ? 'every type' : chosen.map(typeLabel).join(', '));
		onclose();
	}
</script>

<Modal title="Resync {device.name}" {onclose}>
	{#snippet footer()}
		<button class="btn primary" type="submit" form={formId} disabled={busy || (scope === 'some' && chosen.length === 0)}>Request resync</button>
	{/snippet}
	<p class="muted">The device pulls the chosen types again from the start the next time it contacts Vitamux. Samples already stored are not duplicated.</p>
	<ProblemAlert {problem} />
	<form id={formId} onsubmit={submit}>
		<label class="check"><input type="radio" name="scope" value="all" bind:group={scope} /> <span>Every type</span></label>
		<label class="check">
			<input type="radio" name="scope" value="some" bind:group={scope} disabled={device.types.length === 0} />
			<span>Only some types{#if device.types.length === 0}<span class="hint">The device has not reported its types yet.</span>{/if}</span>
		</label>
		{#if scope === 'some'}
			<fieldset>
				<legend>Types to resync</legend>
				{#each device.types as t (t)}
					<label class="check"><input type="checkbox" value={t} bind:group={chosen} /> <span>{typeLabel(t)}</span></label>
				{/each}
			</fieldset>
		{/if}
	</form>
</Modal>
