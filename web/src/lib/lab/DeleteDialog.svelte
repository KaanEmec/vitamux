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

	async function remove(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = await deleteDocument(doc.id, derived);
		busy = false;
		if (!problem) ondeleted(derived);
	}
</script>

<Modal title="Delete document" {onclose}>
	<p>
		Deleting <strong>{doc.filename ?? 'this document'}</strong> destroys the PDF, its filename and every extraction run. This
		cannot be undone.
	</p>
	<ProblemAlert {problem} />
	<form onsubmit={remove}>
		<fieldset>
			<legend>Confirmed results from this document</legend>
			<label class="choice">
				<input type="radio" name="derived" value="keep" bind:group={derived} />
				<span>Keep them <span class="hint">They keep their printed values, page and evidence text.</span></span>
			</label>
			<label class="choice">
				<input type="radio" name="derived" value="delete" bind:group={derived} />
				<span>Delete them too</span>
			</label>
		</fieldset>
		<div class="actions">
			<button class="btn primary" type="submit" disabled={busy}>Delete document</button>
			<button class="btn" type="button" onclick={onclose}>Cancel</button>
		</div>
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
		margin-bottom: var(--space-2);
	}
	.hint {
		display: block;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.actions {
		display: flex;
		gap: var(--space-2);
	}
</style>
