<!--
	Review of one extracted row: edit what was read, accept it, or reject it. Only changed
	fields are sent (merge patch); every change lands in the row's review trail. Warnings
	point at what to compare with the PDF and never rate the value.
-->
<script lang="ts">
	import { untrack } from 'svelte';
	import { fieldErrors, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { patchRow, type Row, type RowPatch } from './api.ts';
	import { rowStatus, warningText, when } from './format.ts';

	let { row, runId, onsaved }: { row: Row; runId: string; onsaved: (row: Row) => void } = $props();

	const textFields = [
		['analyte_label', 'Label as printed'],
		['value_text', 'Value as printed'],
		['unit_text', 'Unit as printed'],
		['reference_range_text', 'Range as printed'],
		['printed_flag', 'Flag as printed'],
		['collected_at', 'Collected'],
		['reported_at', 'Reported'],
		['specimen_type', 'Specimen'],
		['laboratory', 'Laboratory']
	] as const;
	const numberFields = [
		['value_numeric', 'Numeric value'],
		['ref_low', 'Range low'],
		['ref_high', 'Range high']
	] as const;
	type Key = (typeof textFields)[number][0] | (typeof numberFields)[number][0] | 'comparator' | 'analyte';
	const hints: Partial<Record<Key, string>> = {
		unit_text: 'Leave empty to confirm the value as unitless.',
		collected_at: 'YYYY-MM-DD or YYYY-MM-DDTHH:MM, local time as printed.',
		reported_at: 'YYYY-MM-DD or YYYY-MM-DDTHH:MM, local time as printed.',
		printed_flag: 'Only what the report prints, such as H or L.'
	};

	const initial = (r: Row): Record<Key, string> => {
		const s = (v: string | number | null) => (v === null ? '' : String(v));
		return {
			analyte_label: r.analyte_label,
			value_text: s(r.value_text),
			value_numeric: s(r.value_numeric),
			comparator: s(r.comparator),
			unit_text: s(r.unit_text),
			reference_range_text: s(r.reference_range_text),
			ref_low: s(r.ref_low),
			ref_high: s(r.ref_high),
			printed_flag: s(r.printed_flag),
			collected_at: s(r.collected_at),
			reported_at: s(r.reported_at),
			specimen_type: s(r.specimen_type),
			laboratory: s(r.laboratory),
			analyte: s(r.analyte)
		};
	};

	// The parent keys this component by row, so a saved row starts a fresh form.
	let form = $state(untrack(() => initial(row)));

	let busy = $state(false);
	let problem = $state<Problem | null>(null);
	const errors = $derived(fieldErrors(problem));

	/** The changed fields as a merge patch; null for an emptied field. */
	function changes(): { patch: RowPatch; bad: { pointer: string; detail: string }[] } {
		const was = initial(row);
		const patch: Record<string, string | number | null> = {};
		const bad: { pointer: string; detail: string }[] = [];
		for (const k of Object.keys(form) as Key[]) {
			const v = form[k].trim();
			if (v === was[k].trim()) continue;
			if (numberFields.some(([n]) => n === k)) {
				const n = Number(v.replace(',', '.'));
				if (v !== '' && !Number.isFinite(n)) bad.push({ pointer: `/${k}`, detail: 'must be a number' });
				patch[k] = v === '' ? null : n;
			} else if (k === 'analyte_label' && v === '') {
				bad.push({ pointer: '/analyte_label', detail: 'is required' });
			} else {
				patch[k] = v === '' ? null : v;
			}
		}
		return { patch: patch as RowPatch, bad };
	}

	const dirty = $derived.by(() => {
		const was = initial(row);
		return (Object.keys(form) as Key[]).some((k) => form[k].trim() !== was[k].trim());
	});

	async function send(review: 'accept' | 'reject' | null) {
		const { patch, bad } = changes();
		if (bad.length && review !== 'reject') {
			problem = { type: 'about:blank', title: 'Check the highlighted fields', status: 422, code: 'validation_failed', detail: 'Check the highlighted fields.', errors: bad };
			return;
		}
		const body: RowPatch = review === 'reject' ? { review } : { ...patch, ...(review ? { review } : {}) };
		problem = null;
		busy = true;
		const res = await patchRow(runId, row.index, body);
		busy = false;
		if (res.problem) problem = res.problem;
		else onsaved(res.data);
	}

	const offered = $derived(row.suggested_analyte && row.suggested_analyte !== form.analyte.trim() ? row.suggested_analyte : null);
	const notes = $derived([...row.validation, ...row.warnings]);
</script>

<form
	class="editor"
	aria-label="Row {row.index + 1}: {row.analyte_label}"
	onsubmit={(e) => {
		e.preventDefault();
		void send('accept');
	}}
>
	<div class="head">
		<h3>Row {row.index + 1}{#if row.page}, page {row.page}{/if}</h3>
		<span class="state"><StatusIcon status={rowStatus[row.review_status].status} /> {rowStatus[row.review_status].label}</span>
	</div>

	{#if notes.length}
		<ul class="notes" aria-label="Checks for this row">
			{#each notes as code (code)}
				<li><StatusIcon status="warn" /> <span>{warningText(code)}</span></li>
			{/each}
		</ul>
	{/if}
	<p class="evidence"><span class="muted">Printed text:</span> <q>{row.evidence_text}</q></p>
	<ProblemAlert {problem} fields={Object.keys(form)} />

	<div class="grid">
		{#each textFields as [key, label] (key)}
			<TextField {label} name={key} bind:value={form[key]} error={errors[key]} hint={hints[key]} autocomplete="off" />
		{/each}
		{#each numberFields as [key, label] (key)}
			<TextField {label} name={key} bind:value={form[key]} error={errors[key]} inputmode="decimal" autocomplete="off" />
		{/each}
		<div class="field">
			<label for="comparator-{row.index}">Comparator</label>
			<select id="comparator-{row.index}" name="comparator" bind:value={form.comparator}>
				<option value="">None</option>
				<option value="<">&lt;</option>
				<option value=">">&gt;</option>
				<option value="<=">&lt;=</option>
				<option value=">=">&gt;=</option>
			</select>
			{#if errors.comparator}<span class="error">{errors.comparator}</span>{/if}
		</div>
		<div>
			<TextField
				label="Analyte code"
				name="analyte"
				bind:value={form.analyte}
				error={errors.analyte}
				hint="Empty records the analyte as unknown; printed values are kept either way."
				list="analyte-codes"
				autocomplete="off"
			/>
			{#if offered}
				<p class="offer">
					Label alias match: <code>{offered}</code>
					<button class="btn" type="button" onclick={() => (form.analyte = offered ?? '')}>Use {offered}</button>
				</p>
			{:else if row.suggested_analyte}
				<p class="offer muted">From a label alias match; not final until you accept.</p>
			{/if}
		</div>
	</div>

	<div class="actions">
		<button class="btn primary" type="submit" disabled={busy}>{dirty ? 'Save and accept' : 'Accept as read'}</button>
		{#if dirty}
			<button class="btn" type="button" disabled={busy} onclick={() => (form = initial(row))}>Undo changes</button>
		{/if}
		{#if row.review_status !== 'rejected'}
			<button class="btn" type="button" disabled={busy} onclick={() => void send('reject')}>Reject row</button>
		{/if}
	</div>

	{#if row.edits.length}
		<details>
			<summary>Review trail ({row.edits.length})</summary>
			<ol class="trail">
				{#each row.edits as e, i (i)}
					<li>
						{when(e.created_at)}: {e.action === 'edit' ? `edited ${Object.keys(e.changes).join(', ')}` : e.action === 'accept' ? 'accepted' : 'rejected'}
						by {e.actor}
					</li>
				{/each}
			</ol>
		</details>
	{/if}
</form>

<style>
	.editor {
		padding: var(--space-4);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.head {
		display: flex;
		gap: var(--space-3);
		align-items: baseline;
		justify-content: space-between;
	}
	.state {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
	}
	.notes {
		margin: 0 0 var(--space-3);
		padding: 0;
		list-style: none;
	}
	.notes li {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
	}
	.notes :global(.status-icon) {
		margin-top: 0.3em;
	}
	.evidence {
		margin: 0 0 var(--space-3);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(12rem, 1fr));
		gap: 0 var(--space-4);
		align-items: start;
	}
	select {
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.offer {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin: calc(-1 * var(--space-3)) 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.trail {
		margin: var(--space-2) 0 0;
		font-size: var(--text-sm);
	}
</style>
