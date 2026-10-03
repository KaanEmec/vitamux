<!--
	Review of one document: the PDF page with the selected row outlined beside the extracted
	rows. Every row is accepted, edited or rejected before confirm creates the results; a
	confirmed run can be edited and confirmed again (new revisions) or unconfirmed.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import * as lab from '#lib/lab/api.ts';
	import type { Document, Extraction, Row } from '#lib/lab/api.ts';
	import DeleteDialog from '#lib/lab/DeleteDialog.svelte';
	import ExtractDialog from '#lib/lab/ExtractDialog.svelte';
	import { documentStatus, errorClasses, printedValue, providerName, rowOfPointer, rowStatus, runStatus, warningText, when } from '#lib/lab/format.ts';
	import PdfViewer from '#lib/lab/PdfViewer.svelte';
	import RowEditor from '#lib/lab/RowEditor.svelte';

	const id = $derived(page.params.id ?? '');

	let doc = $state<Document | null>(null);
	let runs = $state<Extraction[]>([]);
	let runId = $state('');
	let run = $state<Extraction | null>(null);
	let pdf = $state<ArrayBuffer | null>(null);
	let codes = $state<string[]>([]);
	let selected = $state<number | null>(null);
	let viewPage = $state(1);
	let problem = $state<Problem | null>(null);
	let confirmProblem = $state<Problem | null>(null);
	let notice = $state('');
	let busy = $state(false);
	let extracting = $state(false);
	let deleting = $state(false);

	const rows = $derived(run?.rows ?? []);
	const row = $derived(selected === null ? null : (rows.find((r) => r.index === selected) ?? null));
	const pending = $derived(rows.filter((r) => r.review_status === 'pending').length);
	const active = $derived(runs.some((r) => r.status === 'queued' || r.status === 'running'));
	const reviewable = $derived(run?.status === 'succeeded' || run?.status === 'confirmed');
	const bbox = $derived(row?.bbox && row.page === viewPage ? (row.bbox as { x0: number; y0: number; x1: number; y1: number }) : null);

	async function loadRuns() {
		const r = await lab.listRuns(id);
		const d = await lab.getDocument(id); // after the runs, so its status reflects them
		problem = d.problem ?? r.problem;
		if (d.data) doc = d.data;
		if (!r.data) return;
		runs = r.data;
		if (!runs.some((x) => x.id === runId)) {
			runId = (runs.find((x) => x.status === 'confirmed') ?? runs.find((x) => x.status === 'succeeded') ?? runs[0])?.id ?? '';
		}
		await loadRun();
	}

	async function loadRun() {
		if (!runId) {
			run = null;
			return;
		}
		const res = await lab.getRun(runId);
		if (res.problem) problem = res.problem;
		else run = res.data;
	}

	onMount(() => {
		void (async () => {
			await loadRuns();
			if (doc && doc.status !== 'deleted') {
				const f = await lab.documentFile(id);
				if (f.data) pdf = f.data;
			}
			const a = await lab.listAliases();
			if (a.data) codes = [...new Set(a.data.map((x) => x.analyte))].sort();
		})();
		// While a run is queued or running, check again every two seconds.
		const timer = setInterval(() => {
			if (active) void loadRuns();
		}, 2000);
		return () => clearInterval(timer);
	});

	function select(r: Row) {
		selected = r.index;
		if (r.page) viewPage = r.page;
	}

	async function saved(r: Row) {
		const reviewed = r.review_status !== 'pending';
		await loadRuns();
		confirmProblem = null;
		// Move on to the next row still waiting for review.
		const next = reviewed ? rows.find((x) => x.review_status === 'pending' && x.index > r.index) ?? rows.find((x) => x.review_status === 'pending') : null;
		if (next) select(next);
	}

	async function confirm() {
		if (!run) return;
		busy = true;
		confirmProblem = null;
		notice = '';
		const res = await lab.confirmRun(run.id);
		busy = false;
		if (res.problem) {
			confirmProblem = res.problem;
			return;
		}
		const kept = (res.data.rows ?? []).filter((r) => r.review_status !== 'rejected').length;
		notice = `Confirmed ${kept} result${kept === 1 ? '' : 's'}.`;
		await loadRuns();
	}

	async function unconfirm() {
		if (!run) return;
		busy = true;
		notice = '';
		const res = await lab.unconfirmRun(run.id);
		busy = false;
		if (res.problem) confirmProblem = res.problem;
		else {
			notice = 'Unconfirmed: the results from this run were removed and the rows are back in review.';
			await loadRuns();
		}
	}

	const label = (r: Row | undefined) => (r ? `Row ${r.index + 1} (${r.analyte_label})` : 'The extraction');
</script>

<svelte:head><title>Review · Lab results · Vitamux</title></svelte:head>

<ProblemAlert {problem} />
{#if doc}
	{@const st = documentStatus[doc.status]}
	<div class="top">
		<div>
			<h2>{doc.filename ?? 'Lab document'}</h2>
			<p class="muted">
				<span class="status"><StatusIcon status={st.status} /> {st.label}</span> · uploaded {when(doc.uploaded_at)} · {doc.page_count}
				page{doc.page_count === 1 ? '' : 's'}
			</p>
		</div>
		{#if doc.status !== 'deleted'}
			<div class="actions">
				<button class="btn" type="button" disabled={active} onclick={() => (extracting = true)}>{runs.length ? 'Extract again' : 'Extract'}</button>
				<button class="btn" type="button" onclick={() => (deleting = true)}>Delete document</button>
			</div>
		{/if}
	</div>

	{#if runs.length > 1}
		<div class="field run-picker">
			<label for="run">Extraction run</label>
			<select id="run" bind:value={runId} onchange={() => { selected = null; void loadRun(); }}>
				{#each runs as r (r.id)}
					<option value={r.id}>{when(r.created_at)} · {providerName(r.provider)}{r.model ? ` (${r.model})` : ''} · {runStatus[r.status].label}</option>
				{/each}
			</select>
		</div>
	{/if}

	{#if !run && !active}
		<p class="muted">No extraction yet. Choose Extract to read the printed rows.</p>
	{:else if active && (!run || run.status === 'queued' || run.status === 'running')}
		<p role="status"><StatusIcon status="pending" /> Extraction in progress… {run?.error_class ? errorClasses[run.error_class] ?? '' : ''}</p>
	{:else if run && run.status === 'failed'}
		<p role="status"><StatusIcon status="error" /> The extraction failed. {errorClasses[run.error_class ?? ''] ?? ''}</p>
	{/if}

	{#if run && reviewable}
		{#if run.warnings.length}
			<ul class="doc-notes" aria-label="Checks for this document">
				{#each run.warnings as w (w)}<li><StatusIcon status="warn" /> {warningText(w)}</li>{/each}
			</ul>
		{/if}
		<div class="review">
			<div class="pdf">
				{#if pdf}
					<PdfViewer data={pdf} bind:page={viewPage} {bbox} />
					{#if row && !row.bbox}<p class="muted">No row area was read for this row; compare the printed text instead.</p>{/if}
				{:else if doc.status === 'deleted'}
					<p class="muted">The original PDF was deleted.</p>
				{:else}
					<p class="muted" role="status">Loading PDF…</p>
				{/if}
			</div>

			<div class="rows">
				<p class="muted">
					Read by {providerName(run.provider)}{run.model ? ` (${run.model})` : ''} on {when(run.finished_at ?? run.created_at)}. {pending
						? `${pending} of ${rows.length} rows not reviewed yet.`
						: `All ${rows.length} rows reviewed.`}
				</p>
				<table>
					<caption class="visually-hidden">Extracted rows</caption>
					<thead>
						<tr>
							<th scope="col">Row</th><th scope="col">Label</th><th scope="col">Value</th><th scope="col">Unit</th>
							<th scope="col">Range as printed</th><th scope="col">Flag as printed</th><th scope="col">Analyte</th>
							<th scope="col">Checks</th><th scope="col">Review</th>
						</tr>
					</thead>
					<tbody>
						{#each rows as r (r.index)}
							{@const rs = rowStatus[r.review_status]}
							{@const checks = r.validation.length + r.warnings.length}
							<tr class={[r.index === selected && 'current', r.review_status === 'rejected' && 'rejected']} aria-current={r.index === selected ? 'true' : undefined}>
								<td><button class="btn link" type="button" onclick={() => select(r)} aria-label="Review row {r.index + 1}: {r.analyte_label}">{r.index + 1}</button></td>
								<th scope="row">{r.analyte_label}</th>
								<td>{printedValue(r)}</td>
								<td>{r.unit_text ?? '–'}</td>
								<td>{r.reference_range_text ?? '–'}</td>
								<td>{r.printed_flag ?? '–'}</td>
								<td>{#if r.analyte}<code>{r.analyte}</code>{:else}<span class="muted">unknown</span>{/if}</td>
								<td>{#if checks}<span class="status"><StatusIcon status="warn" /> {checks}</span>{:else}–{/if}</td>
								<td><span class="status"><StatusIcon status={rs.status} /> {rs.label}</span></td>
							</tr>
						{/each}
					</tbody>
				</table>

				{#if row}
					{#key row}<RowEditor {row} runId={run.id} onsaved={saved} />{/key}
				{:else}
					<p class="muted">Select a row to compare it with the PDF and review it.</p>
				{/if}

				<section class="confirm" aria-labelledby="confirm-title">
					<h3 id="confirm-title">Confirm</h3>
					{#if notice}<p role="status"><StatusIcon status="ok" /> {notice} <a href="/lab/results">See results</a></p>{/if}
					{#if confirmProblem?.status === 422}
						<div class="not-ready" role="alert">
							<p><StatusIcon status="error" /> <strong>Not ready to confirm.</strong></p>
							<ul>
								{#each confirmProblem.errors ?? [] as e (e.pointer)}
									{@const n = rowOfPointer(e.pointer)}
									{@const r = rows.find((x) => x.index === n)}
									<li>
										{label(r)}{e.pointer.split('/')[3] ? ` ${e.pointer.split('/')[3].replaceAll('_', ' ')}` : ''}: {e.detail}
										{#if r}<button class="btn link" type="button" onclick={() => select(r)}>Go to row {r.index + 1}</button>{/if}
									</li>
								{/each}
							</ul>
						</div>
					{:else}
						<ProblemAlert problem={confirmProblem} />
					{/if}
					{#if run.status === 'confirmed'}
						<p class="muted">Confirmed. Edits to rows apply after confirming again, which records a new revision of each changed result.</p>
						<div class="actions">
							<button class="btn primary" type="button" disabled={busy} onclick={confirm}>Confirm again</button>
							<button class="btn" type="button" disabled={busy} onclick={unconfirm}>Unconfirm</button>
						</div>
					{:else}
						<p class="muted">Confirming saves every accepted or edited row as a result, with its printed values and page.</p>
						<button class="btn primary" type="button" disabled={busy} onclick={confirm}>Confirm results</button>
					{/if}
				</section>
			</div>
		</div>
	{/if}
{:else if !problem}
	<p class="muted" role="status">Loading…</p>
{/if}

<datalist id="analyte-codes">
	{#each codes as c (c)}<option value={c}></option>{/each}
</datalist>

{#if extracting && doc}
	<ExtractDialog
		{doc}
		onclose={() => (extracting = false)}
		onstarted={(r) => {
			extracting = false;
			runId = r.id;
			selected = null;
			void loadRuns();
		}}
	/>
{/if}
{#if deleting && doc}
	<DeleteDialog {doc} onclose={() => (deleting = false)} ondeleted={() => void goto('/lab')} />
{/if}

<style>
	.top {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: flex-start;
		justify-content: space-between;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.status {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		white-space: nowrap;
	}
	.run-picker select {
		max-width: 36rem;
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.doc-notes {
		padding: 0;
		list-style: none;
	}
	.review {
		display: grid;
		grid-template-columns: minmax(18rem, 5fr) 7fr;
		gap: var(--space-5);
		align-items: start;
	}
	.pdf {
		position: sticky;
		top: var(--space-4);
	}
	@media (max-width: 70rem) {
		.review {
			grid-template-columns: 1fr;
		}
		.pdf {
			position: static;
		}
	}
	.rows {
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	table {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th,
	td {
		padding: var(--space-1) var(--space-2);
		text-align: left;
		border-bottom: 1px solid var(--color-border);
	}
	tr.current {
		background: var(--color-surface-2);
		outline: 2px solid var(--color-focus);
		outline-offset: -2px;
	}
	tr.rejected th,
	tr.rejected td:nth-child(n + 3):not(:last-child) {
		text-decoration: line-through;
		color: var(--color-text-muted);
	}
	.not-ready {
		padding: var(--space-3);
		margin-bottom: var(--space-3);
		background: var(--color-error-bg);
		border: 1px solid var(--color-error);
		border-radius: var(--radius-sm);
	}
	.not-ready ul {
		margin: 0;
	}
</style>
