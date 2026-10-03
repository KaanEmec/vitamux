<!--
	AI providers for lab extraction. Enabling a provider only makes it selectable; a PDF is
	sent to it when the owner consents for that document (docs/architecture/lab-documents.md).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { loadSettings, patchSettings, type SettingsMap } from '#lib/settings/api.ts';
	import Notice from '#lib/settings/Notice.svelte';

	const providers = [
		{ key: 'documents.external_ai.gemini.enabled', name: 'Google Gemini' },
		{ key: 'documents.external_ai.openai.enabled', name: 'OpenAI' },
		{ key: 'documents.external_ai.openai_compatible.enabled', name: 'OpenAI-compatible server' }
	];

	let saved = $state<SettingsMap | null>(null);
	let enabled = $state<Record<string, boolean>>({});
	let problem = $state<Problem | null>(null);
	let done = $state(false);
	let busy = $state(false);

	function adopt(s: SettingsMap) {
		saved = s;
		enabled = Object.fromEntries(providers.map((p) => [p.key, s[p.key] === true]));
	}

	onMount(async () => {
		const res = await loadSettings();
		problem = res.problem;
		if (res.settings) adopt(res.settings);
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!saved) return;
		const patch: SettingsMap = {};
		for (const p of providers) if (enabled[p.key] !== (saved[p.key] === true)) patch[p.key] = enabled[p.key];
		problem = null;
		done = false;
		if (Object.keys(patch).length === 0) {
			done = true;
			return;
		}
		busy = true;
		const res = await patchSettings(patch);
		busy = false;
		problem = res.problem;
		if (res.settings) {
			adopt(res.settings);
			done = true;
		}
	}
</script>

<svelte:head><title>AI providers · Vitamux</title></svelte:head>

<section aria-labelledby="ai">
	<h2 id="ai">AI providers</h2>
	<p class="muted">
		Lab PDFs can be read by an external AI provider that extracts the printed rows for you to review. Nothing is sent
		until you consent for a specific document; enabling a provider here only lets you pick it.
	</p>
	<ProblemAlert {problem} />
	{#if done}<Notice>Saved.</Notice>{/if}

	{#if saved === null && !problem}
		<p class="muted" role="status">Loading settings…</p>
	{:else if saved}
		<form onsubmit={save}>
			{#each providers as p (p.key)}
				<label class="check">
					<input type="checkbox" name={p.key} bind:checked={enabled[p.key]} onchange={() => (done = false)} />
					<span>
						Enable {p.name}
						<span class="hint">
							When you consent for a document, its PDF is sent to {p.name} for extraction. Without that consent, it is not sent.
						</span>
					</span>
				</label>
			{/each}
			<p class="muted">Each document asks for consent again; there is no standing consent.</p>
			<button class="btn primary" type="submit" disabled={busy}>Save</button>
		</form>
	{/if}
</section>
