<!--
	Lab documents: upload, the list with each document's status, and the actions that start
	an extraction, open the review, or delete the document.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import type { Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import { listDocuments, type Document } from '#lib/lab/api.ts';
	import DeleteDialog from '#lib/lab/DeleteDialog.svelte';
	import ExtractDialog from '#lib/lab/ExtractDialog.svelte';
	import { documentStatus, size, when } from '#lib/lab/format.ts';
	import UploadZone from '#lib/lab/UploadZone.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';

	let docs = $state<Document[] | null>(null);
	let problem = $state<Problem | null>(null);
	let extracting = $state<Document | null>(null);
	let deleting = $state<Document | null>(null);
	let notice = $state('');

	async function load() {
		const res = await listDocuments();
		problem = res.problem;
		if (res.data) docs = [...res.data].sort((a, b) => b.uploaded_at.localeCompare(a.uploaded_at));
	}
	onMount(() => void load());

	const name = (d: Document) => d.filename ?? `Document from ${when(d.uploaded_at)}`;
</script>

<svelte:head><title>Lab results · Vitamux</title></svelte:head>

<UploadZone
	onuploaded={() => {
		notice = '';
		void load();
	}}
/>

<section class="card" aria-labelledby="docs">
	<h2 id="docs">Documents</h2>
	<p class="muted lede">
		Each extraction is reviewed row by row against the PDF before anything is saved as a result. Ranges and flags are shown
		as the lab printed them.
	</p>
	<ProblemAlert {problem} />
	{#if notice}<p role="status"><StatusIcon status="ok" /> {notice}</p>{/if}

	{#if docs === null && !problem}
		<p class="muted" role="status">Loading documents…</p>
	{:else if docs && docs.length === 0}
		<EmptyState title="No documents yet." text="Upload a lab report PDF to begin." icon={icons.lab} />
	{:else if docs}
		<div class="wrap">
			<table>
				<caption class="visually-hidden">Documents</caption>
				<thead>
					<tr>
						<th scope="col">Document</th><th scope="col">Uploaded</th><th scope="col">Pages</th><th scope="col">Size</th>
						<th scope="col">Status</th><th scope="col"><span class="visually-hidden">Actions</span></th>
					</tr>
				</thead>
				<tbody>
					{#each docs as d (d.id)}
						{@const st = documentStatus[d.status]}
						<tr>
							<th scope="row">
								{#if d.status === 'deleted'}{name(d)}{:else}<a href="/lab/documents/{d.id}">{name(d)}</a>{/if}
							</th>
							<td>{when(d.uploaded_at)}</td>
							<td>{d.page_count}</td>
							<td>{size(d.size_bytes)}</td>
							<td><span class="status"><StatusIcon status={st.status} /> {st.label}</span></td>
							<td>
								<div class="actions">
									{#if d.status === 'uploaded'}
										<button class="btn" type="button" onclick={() => (extracting = d)}>Extract</button>
									{:else if d.status === 'needs_review' || d.status === 'confirmed' || d.status === 'extracting'}
										<a class="btn" href="/lab/documents/{d.id}">{d.status === 'needs_review' ? 'Review' : 'Open'}</a>
									{/if}
									{#if d.status !== 'deleted'}
										<button class="btn" type="button" onclick={() => (deleting = d)} aria-label="Delete {name(d)}">Delete</button>
									{/if}
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</section>

{#if extracting}
	{@const doc = extracting}
	{@const href = `/lab/documents/${doc.id}`}
	<ExtractDialog {doc} onclose={() => (extracting = null)} onstarted={() => void goto(href)} />
{/if}
{#if deleting}
	{@const doc = deleting}
	<DeleteDialog
		{doc}
		onclose={() => (deleting = null)}
		ondeleted={(derived) => {
			const n = name(doc);
			deleting = null;
			notice = derived === 'keep' ? `Deleted ${n}; its confirmed results are kept.` : `Deleted ${n} and its results.`;
			void load();
		}}
	/>
{/if}

<style>
	.lede {
		margin-top: calc(-1 * var(--space-2));
	}
	.wrap {
		overflow-x: auto;
	}
	table {
		width: 100%;
		border-collapse: collapse;
	}
	th,
	td {
		padding: var(--space-3);
		text-align: left;
		vertical-align: middle;
		border-bottom: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--color-text-muted);
	}
	tbody tr:last-child > * {
		border-bottom: 0;
	}
	.status {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		white-space: nowrap;
	}
	.actions {
		display: flex;
		gap: var(--space-2);
		justify-content: flex-end;
	}
	a.btn {
		text-decoration: none;
	}
</style>
