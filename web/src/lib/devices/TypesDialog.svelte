<!--
	"Choose types…" for one app in a device's source filter (J22.25): one checkbox per type the app
	writes. Every type checked is take, none is ignore, anything between is per_type.
-->
<script lang="ts">
	import Modal from '#lib/components/Modal.svelte';
	import { typeLabel } from './format.ts';
	import { appName, fromTypes, typesOf, type FilterOrigin, type Next } from './sourceFilter.ts';

	let { origin, onclose, onsave }: { origin: FilterOrigin; onclose: () => void; onsave: (next: Next) => void } = $props();

	const all = $derived(typesOf(origin));
	// The initial selection comes from the app's mode when the dialog opens.
	// svelte-ignore state_referenced_locally
	let chosen = $state<string[]>(origin.mode === 'take' ? typesOf(origin) : origin.mode === 'per_type' ? [...origin.types] : []);
	const formId = $props.id();

	function submit(e: SubmitEvent) {
		e.preventDefault();
		onsave(fromTypes(all, chosen));
	}
</script>

<Modal title="Types from {appName(origin)}" {onclose}>
	{#snippet footer()}
		<button class="btn" type="button" onclick={onclose}>Cancel</button>
		<button class="btn primary" type="submit" form={formId}>Save types</button>
	{/snippet}
	<p class="muted">Take only some of what {appName(origin)} writes. Unchecked types stay on the phone. All checked takes the app; none checked ignores it.</p>
	<form id={formId} onsubmit={submit}>
		<fieldset>
			<legend>Types to take</legend>
			{#each all as t (t)}
				<label class="check"><input type="checkbox" value={t} bind:group={chosen} /> <span>{typeLabel(t)}</span></label>
			{/each}
		</fieldset>
	</form>
</Modal>
