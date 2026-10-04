<!--
	Delete a document. The PDF, its filename and extraction runs are destroyed (crypto-shred);
	the owner chooses whether confirmed results derived from it stay (derived=keep) or go too.
-->
<script lang="ts">
	import type { Problem } from '#lib/api/client.ts';
	import Modal from '#lib/components/Modal.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { deleteDocument, type Document } from './api.ts';

	let { doc, onclose, ondeleted }: { doc: Document; onclose: () => void; ondeleted: (derived: 'keep' | 'delete') => void } =
		$props();

	let derived = $state<'keep' | 'delete'>('keep');
	let busy = $state(false);
	let problem = $state<Problem | null>(null);
	const formId = $props.id();

	async function remove(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = await deleteDocument(doc.id, derived);
		busy = false;
		if (!problem) ondeleted(derived);
	}
</script>

<Modal title="Delete document" {onclose}>
	{#snippet footer()}
		<button class="btn" type="button" onclick={onclose}>Cancel</button>
		<button class="btn destructive primary" type="submit" form={formId} disabled={busy}>Delete document</button>
	{/snippet}
	<p>
		Deleting <strong>{doc.filename ?? 'this document'}</strong> destroys the PDF, its filename and every extraction run. This
		cannot be undone.
	</p>
	<ProblemAlert {problem} />
	<form id={formId} onsubmit={remove}>
		<fieldset>
			<legend>Confirmed results from this document</legend>
			<div class="options">
				<label class="option-card check">
					<input type="radio" name="derived" value="keep" bind:group={derived} />
					<span>Keep them <span class="hint">They keep their printed values, page and evidence text.</span></span>
				</label>
				<label class="option-card check">
					<input type="radio" name="derived" value="delete" bind:group={derived} />
					<span>Delete them too</span>
				</label>
			</div>
		</fieldset>
	</form>
</Modal>

<style>
	.options {
		display: grid;
		gap: var(--space-2);
	}
</style>
