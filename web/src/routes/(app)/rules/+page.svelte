<!--
	Rules catalogue: every metric with the rule in effect (the owner's version or the built-in
	default with its reason) and a 90-day per-source coverage heatmap.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import CoverageHeatmap from '#lib/components/CoverageHeatmap.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import { ladder, lastDays, opLabel, windowLabel, type Rule } from '#lib/rules/rule.ts';
	import { getCoverage, type Coverage } from '#lib/rules/stubs.ts';

	type Entry = { metric: string; rule: Schemas['RuleVersion'] | null };

	let entries = $state<Entry[] | null>(null);
	let problem = $state<Problem | null>(null);
	let coverage = $state<Coverage | null>(null);
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

	const spec = (r: Schemas['RuleVersion']) => r.spec as unknown as Rule;
	const rowsFor = (metric: string) =>
		(coverage?.rows ?? []).filter((r) => r.metric === metric).map((r) => ({ label: r.source, days: r.days }));
</script>

<svelte:head><title>Rules · Vitamux</title></svelte:head>

<div class="head">
	<h1>Rules</h1>
	<a class="btn primary" href="/rules/new">New rule</a>
</div>
<p class="muted">
	Which source each metric uses, per window. Built-in defaults apply until you edit a metric; your first edit copies the
	default into your own version 1.
</p>

<ProblemAlert {problem} />

{#if !coverage && entries}
	<p class="note"><StatusIcon status="info" /> Coverage heatmaps are not available yet.</p>
{/if}

{#if entries === null && !problem}
	<p class="muted" role="status">Loading rules…</p>
{:else if entries}
	<ul class="catalogue">
		{#each entries as e (e.metric)}
			<li class="card" aria-labelledby="m-{e.metric}">
				<div class="title">
					<h2 id="m-{e.metric}"><a href="/rules/{e.metric}">{e.metric}</a></h2>
					{#if !e.rule}
						<span class="badge"><StatusIcon status="off" /> No rule</span>
					{:else if e.rule.builtin}
						<span class="badge"><StatusIcon status="info" /> Built-in default</span>
					{:else}
						<span class="badge"><StatusIcon status="ok" /> Your rule · version {e.rule.version}</span>
					{/if}
				</div>
				{#if e.rule}
					{@const r = spec(e.rule)}
					<p class="meta">{windowLabel(r.window)} · {opLabel(r.strategy.op)}</p>
					<p class="ladder"><span class="muted">Order:</span> <code>{ladder(r)}</code></p>
					{#if e.rule.builtin}
						<p class="reason">
							{#if e.rule.reason}{e.rule.reason}{/if}
							<span class="muted">Suggested order; you can reorder or replace it.</span>
						</p>
					{/if}
				{:else}
					<p class="muted">Only the all-sources view shows this metric until you pick a source.</p>
				{/if}
				{#if coverage}
					{@const rows = rowsFor(e.metric)}
					{#if rows.length}
						<CoverageHeatmap {rows} start={coverage.start_date} caption="{e.metric} coverage, last 90 days" />
					{:else}
						<p class="muted small">No data in the last 90 days.</p>
					{/if}
				{/if}
				<div class="actions">
					<a href="/rules/{e.metric}">History</a>
					{#if e.rule}
						<a href="/rules/new?metric={e.metric}">Reorder or edit</a>
						<a href="/rules/new?metric={e.metric}&amp;blank=1">Replace</a>
					{:else}
						<a href="/rules/new?metric={e.metric}&amp;blank=1">Create a rule</a>
					{/if}
				</div>
			</li>
		{/each}
	</ul>
{/if}

<style>
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-4);
	}
	.note {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-2) var(--space-3);
		background: var(--color-info-bg);
		border-radius: var(--radius-sm);
	}
	.catalogue {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(22rem, 1fr));
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.card {
		display: grid;
		gap: var(--space-2);
		align-content: start;
		padding: var(--space-4);
	}
	.card p {
		margin: 0;
	}
	.title {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
	}
	h2 {
		margin: 0;
		font-size: var(--text-md);
		font-family: var(--font-mono);
	}
	.badge {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		font-size: var(--text-sm);
	}
	.meta,
	.reason,
	.small {
		font-size: var(--text-sm);
	}
	.ladder code {
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		font-size: var(--text-sm);
	}
</style>
