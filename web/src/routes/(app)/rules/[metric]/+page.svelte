<!--
	One metric's rule: the version in effect in plain words, 90-day coverage, the version timeline
	with activation, and a side-by-side diff of any two versions. The rule lens beside it edits the
	rule against the last 30 days (a bottom sheet on narrow screens).
-->
<script lang="ts">
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import RuleDiff from '#lib/rules/RuleDiff.svelte';
	import RuleLens from '#lib/rules/RuleLens.svelte';
	import { groupLabel, lastDays, selectorText, type Rule } from '#lib/rules/rule.ts';
	import { ruleSentence } from '#lib/rules/sentence.ts';
	import { getCoverage, type Coverage } from '#lib/rules/stubs.ts';
	import Notice from '#lib/settings/Notice.svelte';
	import Badge from '#lib/ui/Badge.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Chip from '#lib/ui/Chip.svelte';

	type Version = Schemas['RuleVersion'];

	const metric = $derived(page.params.metric ?? '');
	const saved = $derived(page.url.searchParams.get('saved'));

	let versions = $state<Version[] | null>(null);
	let problem = $state<Problem | null>(null);
	let coverage = $state<Coverage | null>(null);
	let origins = $state<Schemas['DataOrigin'][]>([]);
	let origin = $state('');
	let activated = $state<number | null>(null);
	let busy = $state(false);
	let left = $state(0);
	let right = $state(0);
	// Bumped when this page activates a version, so the lens starts again from the new rule.
	let lensKey = $state(0);
	const range = lastDays(90);
	const lensRange = lastDays(30);

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
	});
	$effect(() => {
		const [m, o] = [metric, origin];
		void getCoverage(range.start, range.end, m, o).then((c) => (coverage = c));
	});
	$effect(() => {
		void api.GET('/api/v1/origins').then(({ data }) => (origins = data?.origins ?? []));
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
		lensKey++;
		await load(metric);
	}

	const spec = (v: Version) => v.spec as unknown as Rule;
	const current = $derived(versions?.find((v) => v.active) ?? null);
	const byVersion = (n: number) => versions?.find((v) => v.version === n);
	const name = (v: Version | undefined) => (!v ? '' : v.builtin ? 'Built-in' : `Version ${v.version}`);
	const provider = (g: Rule['groups'][number]) => {
		const p = g.match.find((s) => typeof s.provider === 'string' && s.provider)?.provider;
		return typeof p === 'string' ? p : g.id;
	};
	const rows = $derived(
		(coverage?.rows ?? []).filter((r) => r.metric === metric).map((r) => ({ label: r.source, days: r.days }))
	);
	const when = (s: string | null) => (s ? new Date(s).toLocaleString() : '');
</script>

<svelte:head><title>{metric} rule · Vitamux</title></svelte:head>

<nav class="crumb" aria-label="Breadcrumb"><a href="/rules">Rules</a> <span aria-hidden="true">/</span></nav>
<h1>{metric}</h1>

<div class="layout">
	<div class="main">
		{#if saved}<Notice>Saved version {saved}.</Notice>{/if}
		{#if activated}<Notice>Version {activated} is now active.</Notice>{/if}
		<ProblemAlert {problem} />

		{#if versions === null}
			<p class="muted" role="status">Loading versions…</p>
		{:else}
			{#if current}
				{@const r = spec(current)}
				<section class="card" aria-labelledby="in-effect">
					<h2 id="in-effect">In effect: {current.builtin ? 'built-in default' : `version ${current.version}`}</h2>
					<p class="sentence">{ruleSentence(r)}</p>
					<ol class="groups" aria-label="Source order">
						{#each r.groups as g, i (i)}
							<li><Chip source={provider(g)}>{groupLabel(g.id)}</Chip> <span class="muted">{g.match.map((s) => selectorText(s)).join(' or ')}</span></li>
						{/each}
					</ol>
					{#if r.exclude?.length}
						<p><span class="muted">Never used:</span> {r.exclude.map((s) => selectorText(s)).join('; ')}</p>
					{/if}
					{#if current.builtin}
						<p class="muted">
							{#if current.reason}{current.reason}{/if}
							Suggested order; you can reorder or replace it.
						</p>
					{/if}
					<div class="actions">
						<Button href="/rules/new?metric={metric}">Edit in builder</Button>
						<Button variant="ghost" href="/rules/new?metric={metric}&amp;blank=1">Replace with a new rule</Button>
					</div>
				</section>
			{/if}

			<section class="card" aria-labelledby="coverage">
				<div class="section-head">
					<h2 id="coverage">Coverage, last 90 days</h2>
					{#if origins.length}
						<div class="field inline">
							<label for="coverage-origin">Origin app</label>
							<select id="coverage-origin" bind:value={origin}>
								<option value="">All apps</option>
								{#each origins as o (o.id)}<option value={o.origin_key}>{o.name || o.origin_key}</option>{/each}
							</select>
						</div>
					{/if}
				</div>
				{#if !coverage}
					<p class="muted">Coverage is not available yet.</p>
				{:else if rows.length}
					{#await import('#lib/charts/CoverageStrip.svelte') then { default: CoverageStrip }}<CoverageStrip {rows} start={coverage.start_date} caption="{metric} coverage per source" />{/await}
				{:else}
					<p class="muted">No data in the last 90 days.</p>
				{/if}
			</section>

			{#if versions.length}
				<section class="card" id="history" aria-labelledby="history-title">
					<h2 id="history-title">Version history</h2>
					<ol class="timeline">
						{#each versions as v (v.ref)}
							<li class={{ active: v.active }}>
								<span class="mark" aria-hidden="true"></span>
								<div class="entry">
									<div class="line">
										<code>{v.ref}</code>
										{#if v.active}<Badge tone="accent">Active</Badge>{:else}<span class="muted small">Inactive</span>{/if}
									</div>
									<div class="muted small">
										{v.builtin ? 'Built-in' : when(v.created_at)}{#if v.created_by}{` · ${v.created_by}`}{/if}
									</div>
									{#if v.note || v.based_on}
										<div class="small">{v.note ?? ''}{#if v.based_on}<span class="muted">{` (from ${v.based_on})`}</span>{/if}</div>
									{/if}
								</div>
								<div class="row-actions">
									{#if !v.active && !v.builtin}
										<Button size="sm" disabled={busy} onclick={() => activate(v)}>Activate version {v.version}</Button>
									{/if}
									<Button variant="ghost" size="sm" href="/rules/new?metric={metric}&amp;from={v.version}">Edit a copy</Button>
								</div>
							</li>
						{/each}
					</ol>

					{#if versions.length > 1}
						<h3>Compare versions</h3>
						<div class="compare">
							<div class="field inline">
								<label for="cmp-from">From</label>
								<select id="cmp-from" bind:value={left}>{#each versions as v (v.ref)}<option value={v.version}>{v.ref}</option>{/each}</select>
							</div>
							<div class="field inline">
								<label for="cmp-to">To</label>
								<select id="cmp-to" bind:value={right}>{#each versions as v (v.ref)}<option value={v.version}>{v.ref}</option>{/each}</select>
							</div>
						</div>
						<div class="sides">
							{#each [byVersion(left), byVersion(right)] as v, i (i)}
								<div class="side">
									<span class="muted small">{i === 0 ? 'From' : 'To'} · {name(v)}</span>
									<p>{v ? ruleSentence(spec(v)) : ''}</p>
								</div>
							{/each}
						</div>
						<RuleDiff
							before={byVersion(left)?.spec}
							after={byVersion(right)?.spec}
							caption="Changes from version {left} to version {right}"
							labels={[name(byVersion(left)), name(byVersion(right))]}
						/>
					{/if}
				</section>
			{/if}
		{/if}
	</div>

	{#if metric}
		<div class="side-panel">
			{#key lensKey}
				<RuleLens {metric} start={lensRange.start} end={lensRange.end} ondraft={() => {}} history={false} onsaved={() => load(metric)} />
			{/key}
		</div>
	{/if}
</div>

<style>
	.crumb {
		margin: 0 0 var(--space-1);
		font-size: var(--text-sm);
	}
	h1 {
		font-family: var(--font-mono);
	}
	.layout {
		display: grid;
		grid-template-columns: minmax(0, 1fr) minmax(20rem, 26rem);
		align-items: start;
		gap: var(--space-5);
	}
	.main {
		display: grid;
		gap: var(--space-5);
		min-width: 0;
	}
	.side-panel {
		position: sticky;
		top: var(--space-4);
		max-height: calc(100dvh - var(--space-6));
		overflow: auto;
	}
	@media (max-width: 64rem) {
		.layout {
			grid-template-columns: minmax(0, 1fr);
		}
		.side-panel {
			position: static;
			max-height: none;
			order: -1;
		}
	}
	.card > :global(*:last-child) {
		margin-bottom: 0;
	}
	.card p {
		margin: 0 0 var(--space-3);
	}
	.sentence {
		line-height: 1.55;
	}
	.groups {
		display: grid;
		gap: var(--space-2);
		margin: 0 0 var(--space-3);
		padding: 0;
		list-style: none;
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
	}
	.section-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-3);
		margin-bottom: var(--space-3);
	}
	.section-head h2 {
		margin: 0;
	}
	.field.inline {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		margin: 0;
	}
	.actions,
	.compare,
	.row-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.compare {
		margin-bottom: var(--space-3);
	}
	.small {
		font-size: var(--text-xs);
	}
	.timeline {
		margin: 0 0 var(--space-5);
		padding: 0;
		list-style: none;
	}
	.timeline li {
		position: relative;
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		gap: var(--space-3);
		padding: var(--space-3) 0 var(--space-3) var(--space-5);
		border-left: 2px solid var(--color-border);
	}
	.mark {
		position: absolute;
		top: var(--space-4);
		left: -0.4375rem;
		width: 0.75rem;
		height: 0.75rem;
		background: var(--color-surface);
		border: 2px solid var(--color-text-faint);
		border-radius: 50%;
	}
	.timeline .active .mark {
		background: var(--color-accent);
		border-color: var(--color-accent);
	}
	.entry {
		flex: 1 1 14rem;
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.line {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.sides {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}
	.side {
		padding: var(--space-3);
		font-size: var(--text-sm);
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.side p {
		margin: var(--space-1) 0 0;
	}
</style>
