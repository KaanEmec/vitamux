<!--
	Delete a connection, keeping or deleting its data (DELETE /connections/{id}?data=keep|delete).
	Keep removes the credentials and disables the connection; connecting the same account
	again revives it. Delete also removes its raw payloads, records, cursors, schedules and jobs.
-->
<script lang="ts">
	import { api, type Problem } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import Button from '../ui/Button.svelte';
	import { providerLabel, type Connection } from './connections.ts';

	let {
		connection,
		onclose,
		ondeleted
	}: { connection: Connection; onclose: () => void; ondeleted: (data: 'keep' | 'delete') => void } = $props();

	let data = $state<'keep' | 'delete'>('keep');
	let confirmed = $state(false);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	const formId = $props.id();

	const name = $derived(providerLabel(connection.provider));

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = null;
		const { error } = await api.DELETE('/api/v1/connections/{id}', {
			params: { path: { id: connection.id }, query: { data } }
		});
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		ondeleted(data);
	}
</script>

<Modal title="Remove the {name} connection" {onclose}>
	<form id={formId} onsubmit={submit}>
		<fieldset>
			<legend>What happens to its data?</legend>
			<div class="choices">
				<label class="option-card">
					<input type="radio" name="data" value="keep" bind:group={data} />
					<span><strong>Disconnect and keep the data.</strong> Vitamux forgets the authorization and stops syncing. Everything already collected stays, and connecting the same account again resumes it.</span>
				</label>
				<label class="option-card">
					<input type="radio" name="data" value="delete" bind:group={data} />
					<span><strong>Delete the connection and its data.</strong> Also removes its original provider responses, records, cursors, schedules and job history. This cannot be undone.</span>
				</label>
			</div>
		</fieldset>
		{#if data === 'delete'}
			<label class="check">
				<input type="checkbox" bind:checked={confirmed} />
				<span>I understand that all data from this connection is deleted permanently.</span>
			</label>
		{/if}
		<ProblemAlert {problem} />
	</form>
	{#snippet footer()}
		<Button
			variant={data === 'delete' ? 'danger' : 'primary'}
			class={data === 'delete' ? 'primary' : undefined}
			type="submit"
			form={formId}
			disabled={busy || (data === 'delete' && !confirmed)}
		>
			{data === 'delete' ? 'Delete connection and data' : 'Disconnect and keep data'}
		</Button>
	{/snippet}
</Modal>

<style>
	.choices {
		display: grid;
		gap: var(--space-2);
	}
</style>
