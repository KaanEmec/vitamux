<!--
	Rules catalogue: a card per metric with the rule in effect in plain words (the owner's version
	or the built-in default with its reason), its source order and a 90-day per-source coverage
	heatmap; search and a custom / built-in filter.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import Notice from '#lib/settings/Notice.svelte';
	import { groupLabel, lastDays, type Rule } from '#lib/rules/rule.ts';
	import { ruleSentence } from '#lib/rules/sentence.ts';
	import { getCoverage, type Coverage } from '#lib/rules/stubs.ts';
	import Badge from '#lib/ui/Badge.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Segmented from '#lib/ui/Segmented.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';

	type Entry = { metric: string; rule: Schemas['RuleVersion'] | null };
	type Filter = 'all' | 'custom' | 'builtin' | 'default';

	let entries = $state<Entry[] | null>(null);
	let problem = $state<Problem | null>(null);
	let coverage = $state<Coverage | null>(null);
	let query = $state('');
	let filter = $state<Filter>('all');
	const range = lastDays(90);

	onMount(async () => {
		const [rules, metrics, cov] = await Promise.all([
			api.GET('/api/v1/rules'),
			api.GET('/api/v1/metrics'),
			getCoverage(range.start, range.end)
		]);
		coverage = cov;
		if (rules.error) {
			problem = rules.error;
			return;
		}
		const list: Entry[] = rules.data.rules.map((rule) => ({ metric: rule.metric, rule }));
		// Catalogue codes without any rule (no built-in, never configured) are listed too.
		for (const m of (metrics.data?.metrics ?? []) as { code?: string }[]) {
			if (m.code && !list.some((e) => e.metric === m.code)) list.push({ metric: m.code, rule: null });
		}
		entries = list;
	});

	const isCustom = (e: Entry) => !!e.rule && !e.rule.builtin;
	const isBuiltin = (e: Entry) => !!e.rule?.builtin && !e.rule.default;
	const isDefault = (e: Entry) => !!e.rule?.default;
	const matches: Record<Filter, (e: Entry) => boolean> = { all: () => true, custom: isCustom, builtin: isBuiltin, default: isDefault };
	const count = (f: (e: Entry) => boolean) => entries?.filter(f).length ?? 0;
	const filters = $derived<{ value: Filter; label: string }[]>([
		{ value: 'all', label: `All ${entries?.length ?? 0}` },
		{ value: 'custom', label: `Custom ${count(isCustom)}` },
		{ value: 'builtin', label: `Built-in ${count(isBuiltin)}` },
		{ value: 'default', label: `Default ${count(isDefault)}` }
	]);
	const visible = $derived(
		(entries ?? []).filter((e) => e.metric.includes(query.trim().toLowerCase().replaceAll(' ', '_')) && matches[filter](e))
	);

	const spec = (r: Schemas['RuleVersion']) => r.spec as unknown as Rule;
	/** The provider a group names, for its colour. */
	const provider = (g: Rule['groups'][number]) => {
		const p = g.match.find((s) => typeof s.provider === 'string' && s.provider)?.provider;
		return typeof p === 'string' ? p : g.id;
	};
	const rowsFor = (metric: string) =>
		(coverage?.rows ?? []).filter((r) => r.metric === metric).map((r) => ({ label: r.source, days: r.days }));
</script>

<svelte:head><title>Rules · Vitamux</title></svelte:head>

<div class="head">
	<div>
		<h1>Rules</h1>
		<p class="muted">
			How each metric picks one value when several sources report it. Built-in defaults apply until you edit a metric;
			every change is a new version you can roll back.
		</p>
	</div>
	<Button href="/rules/new">New rule</Button>
</div>

<ProblemAlert {problem} />

{#if entries === null && !problem}
	<div class="grid">
		{#each [1, 2, 3] as i (i)}<Skeleton variant="block" label="Loading rules…" />{/each}
	</div>
{:else if entries}
	<div class="tools">
		<div class="field search">
			<label class="visually-hidden" for="rules-search">Search rules</label>
			<input id="rules-search" type="search" placeholder="Search metric" bind:value={query} />
		</div>
		<Segmented label="Filter" options={filters} bind:value={filter} />
	</div>

	{#if !coverage}
		<Notice status="info">Coverage heatmaps are not available yet.</Notice>
	{/if}

	{#if visible.length === 0}
		<EmptyState title="No rules match" text="Try another name or filter." />
	{/if}
	<ul class="grid">
		{#each visible as e (e.metric)}
			<li class="card" aria-labelledby="m-{e.metric}">
				<div class="title">
					<h2 id="m-{e.metric}"><a href="/rules/{e.metric}">{e.metric}</a></h2>
					{#if !e.rule}
						<Badge>No rule</Badge>
					{:else if e.rule.default}
						<Badge title="Your source order, then a generic device ladder">Default</Badge>
					{:else if e.rule.builtin}
						<Badge>Built-in default</Badge>
					{:else}
						<Badge tone="draft">Your rule · version {e.rule.version}</Badge>
					{/if}
				</div>
				{#if e.rule}
					{@const r = spec(e.rule)}
					<p class="sentence">{ruleSentence(r)}</p>
					<ol class="order" aria-label="Source order">
						{#each r.groups as g, i (i)}<li><Chip source={provider(g)}>{groupLabel(g.id)}</Chip></li>{/each}
					</ol>
				{:else}
					<p class="muted small">Only the all-sources view shows this metric until you pick a source.</p>
				{/if}
				{#if coverage}
					{@const rows = rowsFor(e.metric)}
					{#if rows.length}
						{#await import('#lib/charts/CoverageStrip.svelte') then { default: CoverageStrip }}<CoverageStrip {rows} start={coverage.start_date} caption="{e.metric} coverage, last 90 days" />{/await}
					{:else}
						<p class="muted small">No data in the last 90 days.</p>
					{/if}
				{/if}
				{#if e.rule?.default}
					<p class="muted small">
						{e.rule.reason} Your <a href="/settings/sources#source-order">source order</a> comes first; you can reorder or replace it.
					</p>
				{:else if e.rule?.builtin && e.rule.reason}
					<p class="muted small">{e.rule.reason} Suggested order; you can reorder or replace it.</p>
				{/if}
				<div class="actions">
					{#if e.rule}
						<Button size="sm" href="/rules/{e.metric}">Edit with data</Button>
						<Button variant="ghost" size="sm" href="/rules/{e.metric}#history">History</Button>
					{:else}
						<Button size="sm" href="/rules/new?metric={e.metric}&amp;blank=1">Create a rule</Button>
					{/if}
				</div>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-5);
	}
	.head p {
		max-width: 44rem;
		margin: 0;
	}
	.tools {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}
	.search {
		flex: 1 1 16rem;
		max-width: 22rem;
		margin: 0;
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(min(100%, 22rem), 1fr));
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.card {
		display: flex;
		flex-direction: column;
		gap: var(--space-3);
		min-width: 0;
		padding: var(--space-4) var(--space-5);
	}
	.card p {
		margin: 0;
	}
	.title {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
	}
	h2 {
		margin: 0;
		font-size: var(--text-md);
		font-family: var(--font-mono);
	}
	h2 a {
		color: var(--color-text);
		text-decoration: none;
	}
	h2 a:hover {
		text-decoration: underline;
	}
	.sentence {
		font-size: var(--text-sm);
		line-height: 1.5;
	}
	.order {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.small {
		font-size: var(--text-xs);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-top: auto;
		padding-top: var(--space-1);
	}
</style>
