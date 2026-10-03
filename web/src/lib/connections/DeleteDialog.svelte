<!--
	Delete a connection, keeping or deleting its data (DELETE /connections/{id}?data=keep|delete).
	Keep removes the credentials and disables the connection; connecting the same account
	again revives it. Delete also removes its raw payloads, records, cursors, schedules and jobs.
-->
<script lang="ts">
	import { api, type Problem } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
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
	<form onsubmit={submit}>
		<fieldset>
			<legend>What happens to its data?</legend>
			<label class="choice">
				<input type="radio" name="data" value="keep" bind:group={data} />
				<span><strong>Disconnect and keep the data.</strong> Vitamux forgets the authorization and stops syncing. Everything already collected stays, and connecting the same account again resumes it.</span>
			</label>
			<label class="choice">
				<input type="radio" name="data" value="delete" bind:group={data} />
				<span><strong>Delete the connection and its data.</strong> Also removes its original provider responses, records, cursors, schedules and job history. This cannot be undone.</span>
			</label>
		</fieldset>
		{#if data === 'delete'}
			<label class="choice confirm">
				<input type="checkbox" bind:checked={confirmed} />
				<span>I understand that all data from this connection is deleted permanently.</span>
			</label>
		{/if}
		<ProblemAlert {problem} />
		<button class={['btn', data === 'delete' ? 'danger' : 'primary']} type="submit" disabled={busy || (data === 'delete' && !confirmed)}>
			{data === 'delete' ? 'Delete connection and data' : 'Disconnect and keep data'}
		</button>
	</form>
</Modal>

<style>
	fieldset {
		margin: 0 0 var(--space-4);
		padding: 0;
		border: 0;
	}
	legend {
		margin-bottom: var(--space-2);
		font-weight: 600;
	}
	.choice {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		padding: var(--space-2) 0;
	}
	.choice input {
		margin-top: 0.3em;
	}
	.confirm {
		margin-bottom: var(--space-3);
	}
	.danger {
		color: var(--color-surface);
		background: var(--color-error);
		border-color: var(--color-error);
	}
	.danger:hover {
		background: var(--color-error);
		filter: brightness(0.92);
	}
</style>
