<!--
	A connection's streams: health, newest source time seen, cursor and schedules. Resetting a
	cursor makes the next sync start over from the connector's initial window (stored data
	stays; re-fetched records deduplicate).
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import HealthBadge from '../components/HealthBadge.svelte';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import Button from '../ui/Button.svelte';
	import DataTable from './DataTable.svelte';
	import { ago, every, when, type Connection } from './connections.ts';

	let { connection }: { connection: Connection } = $props();

	let streams = $state<Schemas['Stream'][] | null>(null);
	let problem = $state<Problem | null>(null);
	let resetting = $state<string | null>(null);
	let busy = $state(false);

	async function load() {
		const { data, error } = await api.GET('/api/v1/connections/{id}/streams', { params: { path: { id: connection.id } } });
		problem = error ?? null;
		streams = data?.streams ?? [];
	}

	$effect(() => {
		void load();
	});

	async function reset() {
		if (!resetting) return;
		busy = true;
		const { error } = await api.POST('/api/v1/connections/{id}/streams/{stream}/reset-cursor', {
			params: { path: { id: connection.id, stream: resetting } }
		});
		busy = false;
		resetting = null;
		if (error) {
			problem = error;
			return;
		}
		await load();
	}
</script>

<ProblemAlert {problem} />

{#if streams === null}
	<p class="muted" role="status">Loading streams…</p>
{:else if streams.length}
	<DataTable label="Streams">
		<thead>
			<tr>
				<th scope="col">Stream</th><th scope="col">Health</th><th scope="col">Newest data</th><th scope="col">Cursor</th>
				<th scope="col">Schedules</th><th scope="col"><span class="visually-hidden">Actions</span></th>
			</tr>
		</thead>
		<tbody>
			{#each streams as s (s.name)}
				<tr>
					<th scope="row"><code>{s.name}</code></th>
					<td>
						<HealthBadge health={s.health} />
						{#if s.status_reason || s.health_reason}<div class="muted reason">{s.status_reason ?? s.health_reason}</div>{/if}
					</td>
					<td>{s.high_watermark ? when(s.high_watermark) : '–'}</td>
					<td>{s.has_cursor ? `Saved, ${ago(s.updated_at)}` : 'None (starts from the initial window)'}</td>
					<td>
						{#each s.schedules as sc (sc.id)}
							<div>{sc.mode} {every(sc.interval_seconds)}{sc.enabled ? '' : ' (off)'}</div>
						{:else}–{/each}
					</td>
					<td>
						{#if s.has_cursor && connection.mode !== 'push'}
							<Button size="sm" onclick={() => (resetting = s.name)}>Reset cursor<span class="visually-hidden"> of {s.name}</span></Button>
						{/if}
					</td>
				</tr>
			{/each}
		</tbody>
	</DataTable>
{:else if !problem}
	<p class="muted">No streams yet; they appear after the first sync.</p>
{/if}

{#if resetting}
	<Modal title="Reset the cursor of {resetting}?" onclose={() => (resetting = null)}>
		<p>The next sync fetches this stream again from the connector's initial window. Stored data stays, and records fetched again are deduplicated.</p>
		{#snippet footer()}
			<Button variant="primary" disabled={busy} onclick={reset}>Reset cursor</Button>
		{/snippet}
	</Modal>
{/if}

<style>
	.reason {
		font-size: var(--text-xs);
	}
</style>
