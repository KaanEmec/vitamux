<!--
	Start an extraction: pick a configured provider. An external one sends the PDF off this
	server, so the owner must consent to that provider and model for this document first
	(ADR-0013); the built-in test extractor needs no consent.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Problem } from '#lib/api/client.ts';
	import Modal from '#lib/components/Modal.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { listExtractors, startRun, type Document, type Extraction, type Extractor } from './api.ts';
	import { providerName } from './format.ts';

	let { doc, onclose, onstarted }: { doc: Document; onclose: () => void; onstarted: (run: Extraction) => void } = $props();

	let extractors = $state<Extractor[] | null>(null);
	let chosen = $state('');
	let consent = $state(false);
	let busy = $state(false);
	let problem = $state<Problem | null>(null);

	const selected = $derived(extractors?.find((e) => e.id === chosen) ?? null);
	const label = $derived(doc.filename ?? 'this document');

	async function load() {
		const res = await listExtractors();
		problem = res.problem;
		extractors = res.data;
		const first = res.data?.find((e) => e.enabled);
		if (first && !chosen) chosen = first.id;
	}
	onMount(() => void load());

	async function start(e: SubmitEvent) {
		e.preventDefault();
		if (!selected) return;
		problem = null;
		busy = true;
		const res = await startRun(doc.id, selected);
		busy = false;
		if (res.problem) {
			problem = res.problem;
			if (res.problem.code === 'consent_required') {
				consent = false; // the configured model changed: show the current one and ask again
				await load();
			}
			return;
		}
		onstarted(res.data);
	}
</script>

<Modal title="Extract results" {onclose}>
	<p class="muted">Read the printed rows of {label} into a table that you then review. Nothing is saved as a result until you confirm.</p>
	<ProblemAlert {problem} />
	{#if extractors === null && !problem}
		<p class="muted" role="status">Loading providers…</p>
	{:else if extractors}
		<form onsubmit={start}>
			<fieldset>
				<legend>Extractor</legend>
				{#each extractors as ex (ex.id)}
					<label class={['choice', !ex.enabled && 'off']}>
						<input type="radio" name="provider" value={ex.id} bind:group={chosen} disabled={!ex.enabled} onchange={() => (consent = false)} />
						<span>
							<strong>{providerName(ex.id)}</strong>{#if ex.model}, model <code>{ex.model}</code>{/if}
							<span class="hint">
								{#if !ex.external}
									Runs on this server; reads only the synthetic test PDFs.
								{:else if ex.enabled}
									Sends the PDF to {providerName(ex.id)}.
								{:else}
									Disabled. Enable it under <a href="/settings/ai">Settings → AI providers</a>.
								{/if}
							</span>
						</span>
					</label>
				{/each}
			</fieldset>

			{#if selected?.external}
				<div class="consent" role="group" aria-labelledby="consent-title">
					<h3 id="consent-title">Consent for this document</h3>
					<p>
						Extracting with <strong>{providerName(selected.id)}</strong> sends the whole PDF of {label} to that provider, using
						the model <strong><code>{selected.model}</code></strong>. It leaves this server and is handled under the provider's
						terms. The answer is stored encrypted with the document.
					</p>
					<label class="check">
						<input type="checkbox" bind:checked={consent} />
						<span>I consent to send this PDF to {providerName(selected.id)}, model {selected.model}.</span>
					</label>
				</div>
			{/if}

			<div class="actions">
				<button class="btn primary" type="submit" disabled={busy || !selected || (selected.external && !consent)}>
					{selected?.external ? 'Send and extract' : 'Extract'}
				</button>
				<button class="btn" type="button" onclick={onclose}>Cancel</button>
			</div>
		</form>
	{/if}
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
	.choice,
	.check {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		margin-bottom: var(--space-3);
	}
	.choice.off {
		color: var(--color-text-muted);
	}
	.hint {
		display: block;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.consent {
		padding: var(--space-4);
		margin-bottom: var(--space-4);
		background: var(--color-info-bg);
		border: 1px solid var(--color-info);
		border-radius: var(--radius-sm);
	}
	.actions {
		display: flex;
		gap: var(--space-2);
	}
</style>
