<!--
	Retention: how long lab PDFs, raw provider payloads, superseded rows and ingest
	idempotency records are kept. Known keys have typed controls; any other setting the
	server returns is listed generically under "Other settings" so new keys work at once.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { fieldErrors, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { loadSettings, patchSettings, type SettingsMap } from '#lib/settings/api.ts';
	import Notice from '#lib/settings/Notice.svelte';

	const docDays = 'documents.retention_days';
	const docDelete = 'documents.delete_original_after_confirmation';
	const rawDays = 'retention.raw_days';
	const superseded = 'retention.superseded_after_days';
	const idempotency = 'retention.idempotency_key_days';
	// Shown on other pages (providers, notifications) or by the typed controls here.
	const known = new Set([docDays, docDelete, rawDays, superseded, idempotency]);
	const elsewhere = (k: string) => k.startsWith('documents.external_ai.') || k.startsWith('withings.');

	interface Extra {
		key: string;
		orig: unknown;
		text: string;
		on: boolean;
	}

	let saved = $state<SettingsMap | null>(null);
	let problem = $state<Problem | null>(null);
	let done = $state(false);
	let busy = $state(false);
	let local = $state<Record<string, string>>({});
	const errors = $derived({ ...fieldErrors(problem), ...local });

	let docText = $state('');
	let delOriginal = $state(false);
	let rawRows = $state<{ provider: string; days: string }[]>([]);
	let newProvider = $state('');
	let supText = $state('');
	let idemText = $state('');
	let extras = $state<Extra[]>([]);

	const show = (v: unknown) => (typeof v === 'string' ? v : JSON.stringify(v ?? null));

	function adopt(s: SettingsMap) {
		saved = s;
		docText = typeof s[docDays] === 'number' ? String(s[docDays]) : '';
		delOriginal = s[docDelete] === true;
		const raw = (s[rawDays] ?? {}) as Record<string, number>;
		rawRows = Object.entries(raw).map(([provider, days]) => ({ provider, days: String(days) }));
		supText = typeof s[superseded] === 'number' ? String(s[superseded]) : '';
		idemText = typeof s[idempotency] === 'number' ? String(s[idempotency]) : '';
		extras = Object.entries(s)
			.filter(([k]) => !known.has(k) && !elsewhere(k))
			.map(([key, orig]) => ({ key, orig, text: typeof orig === 'boolean' ? '' : show(orig), on: orig === true }));
	}

	onMount(async () => {
		const res = await loadSettings();
		problem = res.problem;
		if (res.settings) adopt(res.settings);
	});

	function addProvider() {
		const p = newProvider.trim();
		if (p && !rawRows.some((r) => r.provider === p)) rawRows.push({ provider: p, days: '0' });
		newProvider = '';
	}

	/** Parses a whole number in [min, max]; sets a field error and returns undefined otherwise. */
	function whole(key: string, text: string, min: number, max: number): number | undefined {
		const n = Number(text);
		if (text.trim() === '' || !Number.isInteger(n) || n < min || n > max) {
			local[key] = `Enter a whole number from ${min} to ${max}.`;
			return undefined;
		}
		return n;
	}

	function extraValue(x: Extra): unknown {
		if (typeof x.orig === 'boolean') return x.on;
		if (typeof x.orig === 'number') {
			const n = Number(x.text);
			if (x.text.trim() === '' || Number.isNaN(n)) local[x.key] = 'Enter a number.';
			return n;
		}
		if (typeof x.orig === 'string') return x.text;
		if (x.orig === null && x.text.trim() === 'null') return null;
		try {
			return JSON.parse(x.text);
		} catch {
			local[x.key] = 'Enter valid JSON.';
			return undefined;
		}
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!saved) return;
		problem = null;
		done = false;
		local = {};
		const patch: SettingsMap = {};
		const set = (k: string, v: unknown) => {
			if (v !== undefined && JSON.stringify(v) !== JSON.stringify(saved![k] ?? null)) patch[k] = v;
		};

		if (docText.trim() === '') set(docDays, null);
		else set(docDays, whole(docDays, docText, 1, 36500));
		if (delOriginal !== (saved[docDelete] === true)) patch[docDelete] = delOriginal;

		// The server merges per provider; a provider removed from the list is reset to 0 (keep).
		const before = (saved[rawDays] ?? {}) as Record<string, number>;
		const rawPatch: Record<string, number> = {};
		for (const r of rawRows) {
			const n = whole(`${rawDays}.${r.provider}`, r.days === '' ? '0' : r.days, 0, 36500);
			if (n !== undefined && n !== before[r.provider]) rawPatch[r.provider] = n;
		}
		for (const p of Object.keys(before)) if (!rawRows.some((r) => r.provider === p) && before[p] !== 0) rawPatch[p] = 0;
		if (Object.keys(rawPatch).length) patch[rawDays] = rawPatch;

		if (supText.trim() !== '' || typeof saved[superseded] === 'number') {
			const n = whole(superseded, supText === '' ? '0' : supText, 0, 36500);
			if (n !== undefined && n !== (saved[superseded] ?? 0)) patch[superseded] = n;
		}
		if (idemText.trim() !== '') set(idempotency, whole(idempotency, idemText, 7, 36500));

		for (const x of extras) set(x.key, extraValue(x));

		if (Object.keys(local).length) return;
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

	const changed = () => (done = false);
</script>

<svelte:head><title>Retention · Vitamux</title></svelte:head>

<h2>Retention</h2>
<p class="muted">
	Pruned data cannot be recovered. Leave a field empty or at 0 to keep that data. Deletions run in the background after you save.
</p>
<ProblemAlert {problem} fields={[docDays, docDelete, rawDays, superseded, idempotency, ...extras.map((x) => x.key)]} />
{#if done}<Notice>Saved.</Notice>{/if}

{#if saved === null && !problem}
	<p class="muted" role="status">Loading settings…</p>
{:else if saved}
	<form onsubmit={save} oninput={changed}>
		<section aria-labelledby="lab-pdfs">
			<h3 id="lab-pdfs">Lab PDFs</h3>
			<TextField
				label="Delete originals after (days)"
				name={docDays}
				bind:value={docText}
				error={errors[docDays]}
				hint="Counted from upload, for every stored document. Empty keeps them."
				inputmode="numeric"
			/>
			<label class="check">
				<input type="checkbox" name={docDelete} bind:checked={delOriginal} />
				<span>
					Delete the PDF once its extraction is confirmed
					<span class="hint">The confirmed results are kept.</span>
				</span>
			</label>
			{#if errors[docDelete]}<div class="field"><span class="error">{errors[docDelete]}</span></div>{/if}
		</section>

		<section aria-labelledby="raw">
			<h3 id="raw">Raw provider payloads</h3>
			<Notice status="warn">
				Pruned raw payloads can no longer be reprocessed: normalization fixes and new rules cannot be re-run on them. The
				server still keeps raw that reprocessing needs.
			</Notice>
			<p class="muted">Days to keep the original responses per provider. 0 or no row keeps them.</p>
			{#if rawRows.length}
				<table>
					<caption class="visually-hidden">Raw payload retention per provider</caption>
					<thead><tr><th scope="col">Provider</th><th scope="col">Days (0 keeps)</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr></thead>
					<tbody>
						{#each rawRows as r, i (r.provider)}
							<tr>
								<th scope="row">{r.provider}</th>
								<td>
									<div class="field">
										<label class="visually-hidden" for="raw-{r.provider}">Days to keep raw payloads for {r.provider}</label>
										<input id="raw-{r.provider}" name="{rawDays}.{r.provider}" bind:value={r.days} inputmode="numeric" aria-invalid={errors[`${rawDays}.${r.provider}`] ? 'true' : undefined} />
										{#if errors[`${rawDays}.${r.provider}`]}<span class="error">{errors[`${rawDays}.${r.provider}`]}</span>{/if}
									</div>
								</td>
								<td>
									<button class="btn" type="button" onclick={() => { rawRows.splice(i, 1); changed(); }} aria-label="Stop pruning {r.provider}">Keep all</button>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
			<div class="row-form">
				<TextField label="Add a provider" name="new_provider" bind:value={newProvider} hint="Provider code, such as withings." autocomplete="off" />
				<button class="btn" type="button" onclick={addProvider} disabled={!newProvider.trim()}>Add</button>
			</div>
		</section>

		<section aria-labelledby="other-data">
			<h3 id="other-data">Replaced and replayed records</h3>
			<TextField
				label="Keep superseded rows (days)"
				name={superseded}
				bind:value={supText}
				error={errors[superseded]}
				hint="Older versions of corrected records. 0 or empty keeps them."
				inputmode="numeric"
			/>
			<TextField
				label="Keep ingest replay records (days)"
				name={idempotency}
				bind:value={idemText}
				error={errors[idempotency]}
				hint="Stored responses for repeated uploads (Idempotency-Key). At least 7; the default is 30."
				inputmode="numeric"
			/>
		</section>

		{#if extras.length}
			<section aria-labelledby="other">
				<h3 id="other">Other settings</h3>
				{#each extras as x (x.key)}
					{#if typeof x.orig === 'boolean'}
						<label class="check">
							<input type="checkbox" name={x.key} bind:checked={x.on} />
							<span><code>{x.key}</code>{#if errors[x.key]}<span class="hint">{errors[x.key]}</span>{/if}</span>
						</label>
					{:else}
						<TextField label={x.key} name={x.key} bind:value={x.text} error={errors[x.key]} />
					{/if}
				{/each}
			</section>
		{/if}

		<button class="btn primary" type="submit" disabled={busy}>Save retention</button>
	</form>
{/if}
