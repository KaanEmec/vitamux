<!--
	PDF upload by drag and drop or the file picker. Type and size are checked here only to
	answer early; the server's refusal (413, or 422 with the reason) is shown as it comes.
-->
<script lang="ts">
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { maxUploadBytes, upload, type Document } from './api.ts';
	import { uploadReasons } from './format.ts';

	const uploadIcon = 'M12 16V4 M7 9l5-5 5 5 M4 16v4h16v-4';

	let { onuploaded }: { onuploaded: (doc: Document, existing: boolean) => void } = $props();

	let input: HTMLInputElement;
	let over = $state(false);
	let busy = $state(false);
	let problem = $state<Problem | null>(null);
	let notice = $state('');

	const localProblem = (detail: string, status = 422): Problem => ({ type: 'about:blank', title: detail, status, detail, code: 'validation_failed' });

	async function send(file: File | undefined) {
		problem = null;
		notice = '';
		if (!file) return;
		if (file.type !== 'application/pdf' && !file.name.toLowerCase().endsWith('.pdf')) {
			problem = localProblem(uploadReasons.not_pdf);
			return;
		}
		if (file.size > maxUploadBytes) {
			problem = localProblem('The PDF is larger than 20 MiB.', 413);
			return;
		}
		busy = true;
		const res = await upload(file);
		busy = false;
		input.value = '';
		if (res.problem) {
			const reason = res.problem.errors?.[0]?.detail ?? '';
			problem =
				res.problem.status === 413
					? { ...res.problem, detail: 'The PDF is larger than the server accepts (20 MiB).' }
					: uploadReasons[reason]
						? { ...res.problem, detail: uploadReasons[reason], errors: undefined }
						: res.problem;
			return;
		}
		notice = res.data.existing ? `${file.name} was already stored; showing the existing document.` : `Stored ${file.name}.`;
		onuploaded(res.data.doc, res.data.existing);
	}

	function drop(e: DragEvent) {
		e.preventDefault();
		over = false;
		void send(e.dataTransfer?.files[0]);
	}
</script>

<div
	class={['zone', over && 'over']}
	role="group"
	aria-labelledby="upload-title"
	ondragover={(e) => {
		e.preventDefault();
		over = true;
	}}
	ondragleave={() => (over = false)}
	ondrop={drop}
>
	<span class="art"><Icon d={uploadIcon} size={24} /></span>
	<div class="copy">
		<h2 id="upload-title">Upload a lab report</h2>
		<p class="muted">Drop a PDF here or choose one. At most 20 MiB and 50 pages; password-protected PDFs are refused.</p>
		<p class="muted fine">
			The PDF stays on this server. It is sent to an AI provider only if you pick one and consent for that document, naming the provider and model.
		</p>
	</div>
	<label class="btn primary">
		{busy ? 'Uploading…' : 'Choose a PDF'}
		<input
			bind:this={input}
			class="visually-hidden"
			type="file"
			accept="application/pdf,.pdf"
			disabled={busy}
			onchange={() => void send(input.files?.[0])}
		/>
	</label>
</div>
<ProblemAlert {problem} />
{#if notice}<p class="inline-alert ok" role="status"><StatusIcon status="ok" /> <span>{notice}</span></p>{/if}

<style>
	.zone {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: center;
		padding: var(--space-5);
		margin-bottom: var(--space-4);
		background: var(--color-surface);
		border: 2px dashed var(--color-border-strong);
		border-radius: var(--radius-lg);
	}
	.zone.over {
		border-color: var(--color-accent);
		background: var(--color-accent-soft);
	}
	.art {
		display: grid;
		place-items: center;
		width: 3rem;
		height: 3rem;
		color: var(--color-accent);
		background: var(--color-accent-soft);
		border-radius: var(--radius-md);
	}
	.copy {
		flex: 1 1 18rem;
	}
	.copy h2 {
		margin-bottom: var(--space-1);
	}
	.copy p {
		margin: 0;
		max-width: 44rem;
	}
	.fine {
		margin-top: var(--space-1);
		font-size: var(--text-sm);
	}
	.zone label:focus-within {
		outline: 2px solid var(--color-focus);
		outline-offset: 2px;
	}
</style>
