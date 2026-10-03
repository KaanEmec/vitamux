<!--
	One metric's rule: the version in effect, 90-day coverage, version history with a field
	diff between any two versions, and activation of an older or newer version.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import CoverageHeatmap from '#lib/components/CoverageHeatmap.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import RuleDiff from '#lib/rules/RuleDiff.svelte';
	import { lastDays, opLabel, selectorText, windowLabel, type Rule } from '#lib/rules/rule.ts';
	import { getCoverage, type Coverage } from '#lib/rules/stubs.ts';

	type Version = Schemas['RuleVersion'];

	const metric = $derived(page.params.metric ?? '');
	const saved = $derived(page.url.searchParams.get('saved'));

	let versions = $state<Version[] | null>(null);
	let problem = $state<Problem | null>(null);
	let coverage = $state<Coverage | null>(null);
	let activated = $state<number | null>(null);
	let busy = $state(false);
	let left = $state(0);
	let right = $state(0);
	const range = lastDays(90);

	async function load(m: string) {
		const { data, error } = await api.GET('/api/v1/rules/{metric}/versions', { params: { path: { metric: m } } });
		if (error) {
			problem = error;
			versions = [];
			return;
		}
		versions = data.versions;
		const active = data.versions.find((v) => v.active) ?? data.versions[0];
		left = active?.version ?? 0;
		right = data.versions.find((v) => v.version !== left)?.version ?? left;
	}

	$effect(() => {
		const m = metric;
		versions = null;
		problem = null;
		activated = null;
		void load(m);
		void getCoverage(range.start, range.end, m).then((c) => (coverage = c));
	});

	async function activate(v: Version) {
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/rules/{metric}/activate', {
			params: { path: { metric } },
			body: { version: v.version }
		});
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		activated = data.version;
		await load(metric);
	}

	const spec = (v: Version) => v.spec as unknown as Rule;
	const current = $derived(versions?.find((v) => v.active) ?? null);
	const byVersion = (n: number) => versions?.find((v) => v.version === n);
	const rows = $derived(
		(coverage?.rows ?? []).filter((r) => r.metric === metric).map((r) => ({ label: r.source, days: r.days }))
	);
	const when = (s: string | null) => (s ? new Date(s).toLocaleString() : '');
</script>

<svelte:head><title>{metric} rule · Vitamux</title></svelte:head>

<p class="crumb"><a href="/rules">Rules</a> /</p>
<h1>{metric}</h1>

{#if saved}
	<p class="ok" role="status"><StatusIcon status="ok" /> Saved version {saved}.</p>
{/if}
{#if activated}
	<p class="ok" role="status"><StatusIcon status="ok" /> Version {activated} is now active.</p>
{/if}
<ProblemAlert {problem} />

{#if versions === null}
	<p class="muted" role="status">Loading versions…</p>
{:else}
	{#if current}
		{@const r = spec(current)}
		<section class="card" aria-labelledby="in-effect">
			<h2 id="in-effect">In effect: {current.builtin ? 'built-in default' : `version ${current.version}`}</h2>
			<p>{windowLabel(r.window)} · {opLabel(r.strategy.op)}</p>
			{#if current.builtin}
				<p>
					{#if current.reason}{current.reason}{/if}
					<span class="muted">Suggested order; you can reorder or replace it.</span>
				</p>
			{/if}
			<ol class="groups">
				{#each r.groups as g, i (i)}
					<li><code>{g.id}</code> <span class="muted">{g.match.map(selectorText).join(' or ')}</span></li>
				{/each}
			</ol>
			{#if r.exclude?.length}
				<p><span class="muted">Excluded:</span> {r.exclude.map(selectorText).join('; ')}</p>
			{/if}
			<div class="actions">
				<a class="btn primary" href="/rules/new?metric={metric}">Edit in builder</a>
				<a class="btn" href="/rules/new?metric={metric}&amp;blank=1">Replace with a new rule</a>
			</div>
		</section>
	{/if}

	<section aria-labelledby="coverage">
		<h2 id="coverage">Coverage, last 90 days</h2>
		{#if !coverage}
			<p class="muted">Coverage is not available yet.</p>
		{:else if rows.length}
			<CoverageHeatmap {rows} start={coverage.start_date} caption="{metric} coverage per source" />
		{:else}
			<p class="muted">No data in the last 90 days.</p>
		{/if}
	</section>

	{#if versions.length}
		<section aria-labelledby="history">
			<h2 id="history">Version history</h2>
			<table class="versions">
				<thead>
					<tr><th scope="col">Version</th><th scope="col">Status</th><th scope="col">Saved</th><th scope="col">Note</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr>
				</thead>
				<tbody>
					{#each versions as v (v.ref)}
						<tr>
							<th scope="row"><code>{v.ref}</code></th>
							<td>
								{#if v.active}<StatusIcon status="ok" /> Active{:else}<StatusIcon status="off" /> Inactive{/if}
							</td>
							<td>{v.builtin ? 'Built-in' : when(v.created_at)}{#if v.created_by}<span class="muted">{` · ${v.created_by}`}</span>{/if}</td>
							<td>{v.note ?? ''}{#if v.based_on}<span class="muted">{` (from ${v.based_on})`}</span>{/if}</td>
							<td class="row-actions">
								{#if !v.active && !v.builtin}
									<button class="btn" type="button" disabled={busy} onclick={() => activate(v)}>Activate version {v.version}</button>
								{/if}
								<a href="/rules/new?metric={metric}&amp;from={v.version}">Edit a copy</a>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>

			{#if versions.length > 1}
				<h3>Compare versions</h3>
				<div class="compare">
					<label>From <select bind:value={left}>{#each versions as v (v.ref)}<option value={v.version}>{v.ref}</option>{/each}</select></label>
					<label>To <select bind:value={right}>{#each versions as v (v.ref)}<option value={v.version}>{v.ref}</option>{/each}</select></label>
				</div>
				<RuleDiff before={byVersion(left)?.spec} after={byVersion(right)?.spec} caption="Changes from version {left} to version {right}" />
			{/if}
		</section>
	{/if}
{/if}

<style>
	.crumb {
		margin: 0;
		font-size: var(--text-sm);
	}
	h1 {
		font-family: var(--font-mono);
	}
	section {
		margin-bottom: var(--space-6);
	}
	.card p {
		margin: 0 0 var(--space-2);
	}
	.ok {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	.groups {
		margin: 0 0 var(--space-3);
		padding-left: var(--space-5);
		font-size: var(--text-sm);
	}
	.actions,
	.compare,
	.row-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: center;
	}
	.compare {
		margin-bottom: var(--space-3);
	}
	.versions {
		width: 100%;
		border-collapse: collapse;
		margin-bottom: var(--space-4);
		font-size: var(--text-sm);
	}
	.versions th,
	.versions td {
		padding: var(--space-2);
		border-bottom: 1px solid var(--color-border);
		text-align: left;
	}
	select {
		font: inherit;
		padding: var(--space-1) var(--space-2);
		margin-left: var(--space-1);
	}
</style>
