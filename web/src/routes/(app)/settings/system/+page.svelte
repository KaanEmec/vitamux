<!--
	System: versions, database and blob sizes, degraded connections and failing jobs. The
	version comes from /system/version; the rest from /system/status, whose shape is read
	defensively (see #lib/settings/status.ts). Without it the page says so.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import Card from '#lib/settings/Card.svelte';
	import { bytes, when } from '#lib/settings/format.ts';
	import { asNumber, facts, isObj, lastBackup, list, loadStatus, pick, type Obj } from '#lib/settings/status.ts';

	let version = $state<{ version?: string; commit?: string } | null>(null);
	let status = $state<Obj | null>(null);
	let statusProblem = $state<Problem | null>(null);
	let versionProblem = $state<Problem | null>(null);
	let loaded = $state(false);

	onMount(async () => {
		const [v, s] = await Promise.all([api.GET('/api/v1/system/version'), loadStatus()]);
		version = v.data ?? null;
		versionProblem = v.error ?? null;
		status = s.status;
		statusProblem = s.problem;
		loaded = true;
	});

	// Other versions the status may report (database, schema, …), shown beside the build.
	const versions = $derived.by(() => {
		const v = pick(status, 'versions');
		return isObj(v) ? Object.entries(v).filter(([, x]) => typeof x === 'string') : [];
	});
	const dbSize = $derived(asNumber(pick(status, 'database_size_bytes', 'db_size_bytes', 'database.size_bytes', 'db.size_bytes', 'sizes.database_bytes', 'sizes.db_bytes')));
	const blobSize = $derived(asNumber(pick(status, 'blob_size_bytes', 'blobs_size_bytes', 'blob.size_bytes', 'blobs.size_bytes', 'sizes.blob_bytes', 'sizes.blobs_bytes')));
	const backupAt = $derived(lastBackup(status));
	const degraded = $derived(list(status, 'degraded_connections', 'connections.degraded'));
	const failing = $derived(list(status, 'failing_jobs', 'jobs.failing'));
	const title = (o: Obj) => String(o.name ?? o.display_name ?? o.id ?? o.connection_id ?? o.kind ?? 'item');
</script>

<svelte:head><title>System · Vitamux</title></svelte:head>

<p class="lede">Versions, storage and anything that needs attention on this server.</p>

<Card title="Versions" id="versions">
	<ProblemAlert problem={versionProblem} />
	{#if version}
		<dl>
			<dt>Vitamux</dt><dd><code>{version.version}</code></dd>
			<dt>Commit</dt><dd><code>{version.commit}</code></dd>
			{#each versions as [k, v] (k)}<dt>{k}</dt><dd><code>{v}</code></dd>{/each}
		</dl>
	{/if}
</Card>

{#if !loaded}
	<p class="muted" role="status">Loading system status…</p>
{:else if statusProblem}
	<p class="muted"><StatusIcon status="info" /> System status is not available from this server yet.</p>
{:else}
	<Card title="Storage" id="storage">
		<dl>
			<dt>Database</dt><dd>{bytes(dbSize)}</dd>
			<dt>Blobs (PDFs, raw files, exports)</dt><dd>{bytes(blobSize)}</dd>
			<dt>Last backup</dt><dd>{backupAt ? when(backupAt) : 'None recorded'}</dd>
		</dl>
	</Card>

	<Card title="Degraded connections" id="degraded">
		{#if degraded.length === 0}
			<p><StatusIcon status="ok" /> No connection is degraded.</p>
		{:else}
			<ul class="items">
				{#each degraded as d, i (i)}
					<li>
						<StatusIcon status="warn" /> <strong>{title(d)}</strong>
						{#if typeof d.id === 'string' && d.id.startsWith('conn_')}<a href="/connections/{d.id}">Open</a>{/if}
						<span class="muted">{facts(d).filter((f) => f.key !== 'name').map((f) => `${f.key}: ${f.value}`).join(' · ')}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>

	<Card title="Failing jobs" id="failing">
		{#if failing.length === 0}
			<p><StatusIcon status="ok" /> No job is failing.</p>
		{:else}
			<ul class="items">
				{#each failing as j, i (i)}
					<li>
						<StatusIcon status="error" /> <strong>{title(j)}</strong>
						<span class="muted">{facts(j).filter((f) => f.key !== 'kind').map((f) => `${f.key}: ${f.value}`).join(' · ')}</span>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
{/if}

<style>
	dl {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-2) var(--space-5);
		margin: 0;
	}
	dt {
		color: var(--color-text-muted);
	}
	dd {
		margin: 0;
	}
	.items {
		display: grid;
		gap: var(--space-2);
		padding: 0;
		margin: 0;
		list-style: none;
	}
</style>
