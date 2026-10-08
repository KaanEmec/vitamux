<!--
	The provenance chain of one record (GET /provenance/{entity}/{id}): every earlier version,
	the record itself and every later version, each with its connection, pushing client, batch,
	raw payload metadata and normalizer. Raw bodies are never shown.
-->
<script lang="ts" module>
	export type ProvenanceEntity = 'measurement' | 'group' | 'sleep' | 'workout';
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import { when } from '../settings/format.ts';
	import Modal from './Modal.svelte';
	import ProblemAlert from './ProblemAlert.svelte';

	let { entity, id, onclose }: { entity: ProvenanceEntity; id: string; onclose: () => void } = $props();

	type Version = Schemas['ProvenanceVersion'];

	let trace = $state<Schemas['Provenance'] | null>(null);
	let problem = $state<Problem | null>(null);

	onMount(async () => {
		const res = await api.GET('/api/v1/provenance/{entity}/{id}', { params: { path: { entity, id } } });
		if (res.data) trace = res.data;
		else problem = res.error;
	});

	const chain = $derived(
		trace
			? [
					...trace.earlier.map((v) => ({ v, role: 'Earlier version' })),
					{ v: trace.row, role: 'This version' },
					...trace.later.map((v) => ({ v, role: 'Later version' }))
				]
			: []
	);

	const short = (sha: string) => sha.slice(0, 12);

	function versionState(v: Version): string {
		if (v.deleted_at) return `Deleted upstream ${when(v.deleted_at)}`;
		if (v.superseded_at) return `Superseded ${when(v.superseded_at)}`;
		return 'Active';
	}
</script>

<Modal title="Provenance of {entity} {id}" {onclose}>
	<ProblemAlert {problem} />
	{#if !trace && !problem}<p class="muted">Loading the chain…</p>{/if}
	<ol class="chain">
		{#each chain as { v, role } (v.id)}
			<li class:current={role === 'This version'}>
				<h3>{role} <span class="muted">#{v.id}</span></h3>
				<dl>
					<dt>State</dt><dd>{versionState(v)}</dd>
					<dt>Source</dt><dd>{v.provider} · connection <code>{v.connection_id}</code> ({v.connection_mode})</dd>
					{#if v.client}<dt>Pushed by</dt><dd>{v.client.name} ({v.client.kind})</dd>{/if}
					{#if v.batch}
						<dt>Batch</dt><dd><code>{v.batch.id}</code> · {v.batch.source_kind} · received {when(v.batch.received_at)}</dd>
					{/if}
					{#if v.raw}
						<dt>Raw payload</dt>
						<dd>
							<code>{v.raw.id}</code> · stream {v.raw.stream} · key <code>{v.raw.external_key}</code> · version {v.raw.version}
							· {v.raw.size_bytes} bytes · sha256 <code>{short(v.raw.content_sha256)}</code> · {v.raw.status}
						</dd>
					{:else}
						<dt>Raw payload</dt><dd>None (migrated without raw)</dd>
					{/if}
					<dt>Normalizer</dt><dd>{v.normalizer.name}@{v.normalizer.version} <span class="muted">({v.normalizer.git_sha.slice(0, 8)})</span></dd>
					<dt>Fetched</dt><dd>{when(v.fetched_at)}</dd>
					<dt>Ingested</dt><dd>{when(v.ingested_at)}</dd>
					<dt>Normalized</dt><dd>{when(v.normalized_at)}</dd>
					{#if v.corrected_at}<dt>Corrected</dt><dd>{when(v.corrected_at)}</dd>{/if}
					{#if v.superseded_by}<dt>Replaced by</dt><dd>#{v.superseded_by}</dd>{/if}
					{#if v.deleted_by}<dt>Deleted by raw</dt><dd><code>{v.deleted_by.raw_id}</code></dd>{/if}
				</dl>
				<details>
					<summary>Stored row</summary>
					<pre>{JSON.stringify(v.record, null, 2)}</pre>
				</details>
			</li>
		{/each}
	</ol>
</Modal>

<style>
	.chain {
		display: grid;
		gap: var(--space-3);
		margin: var(--space-3) 0 0;
		padding: 0;
		list-style: none;
		overflow-y: auto;
		max-height: 60vh;
	}
	li {
		padding: var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	li.current {
		border-color: var(--color-accent);
		box-shadow: inset 3px 0 0 var(--color-accent);
	}
	h3 {
		font-size: var(--text-md);
	}
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-3);
		margin: 0;
		font-size: var(--text-sm);
	}
	dt {
		color: var(--color-text-muted);
	}
	dd {
		margin: 0;
		overflow-wrap: anywhere;
	}
	pre {
		overflow-x: auto;
		font-size: var(--text-xs);
	}
</style>
