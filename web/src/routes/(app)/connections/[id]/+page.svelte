<!--
	One connection: header with health, then tabs (?tab=overview|streams|backfills|history|settings)
	as links, so each tab has its own URL. Each tab loads its own data.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { api, type Problem } from '#lib/api/client.ts';
	import HealthBadge from '#lib/components/HealthBadge.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import UnofficialBadge from '#lib/components/UnofficialBadge.svelte';
	import Tabs from '#lib/ui/Tabs.svelte';
	import BackfillsTab from '#lib/connections/BackfillsTab.svelte';
	import HistoryTab from '#lib/connections/HistoryTab.svelte';
	import OverviewTab from '#lib/connections/OverviewTab.svelte';
	import SettingsTab from '#lib/connections/SettingsTab.svelte';
	import StreamsTab from '#lib/connections/StreamsTab.svelte';
	import { providerLabel, type Connection } from '#lib/connections/connections.ts';
	import { loadProviders } from '#lib/connections/providers.svelte.ts';

	const tabs = [
		{ id: 'overview', label: 'Overview' },
		{ id: 'streams', label: 'Streams' },
		{ id: 'backfills', label: 'Backfills' },
		{ id: 'history', label: 'History' },
		{ id: 'settings', label: 'Settings' }
	];

	const id = $derived(page.params.id ?? '');
	const tab = $derived.by(() => {
		const t = page.url.searchParams.get('tab');
		return tabs.some((x) => x.id === t) ? (t as string) : 'overview';
	});

	let connection = $state<Connection | null>(null);
	let problem = $state<Problem | null>(null);

	async function load(cid: string) {
		const { data, error } = await api.GET('/api/v1/connections/{id}', { params: { path: { id: cid } } });
		problem = error ?? null;
		if (data) connection = data;
	}

	void loadProviders();

	$effect(() => {
		const cid = id;
		connection = null;
		void load(cid);
	});

	const name = $derived(connection ? providerLabel(connection.provider) : 'Connection');
	const set = (c: Connection) => (connection = c);
</script>

<svelte:head><title>{name} · Vitamux</title></svelte:head>

<p class="crumb"><a href="/connections">Connections</a> /</p>
<div class="head">
	<h1>{name}</h1>
	{#if connection}
		{#if connection.official === false}<UnofficialBadge />{/if}
		<HealthBadge health={connection.health} />
	{/if}
</div>

<ProblemAlert {problem} />

{#if connection}
	<Tabs label="Connection sections" items={tabs.map((t) => ({ href: `?tab=${t.id}`, label: t.label, current: tab === t.id }))} />

	<section aria-label={tabs.find((t) => t.id === tab)?.label}>
		{#if tab === 'overview'}
			<OverviewTab {connection} onchange={set} />
		{:else if tab === 'streams'}
			<StreamsTab {connection} />
		{:else if tab === 'backfills'}
			<BackfillsTab {connection} />
		{:else if tab === 'history'}
			<HistoryTab {connection} />
		{:else}
			<SettingsTab {connection} onchange={set} />
		{/if}
	</section>
{:else if !problem}
	<p class="muted" role="status">Loading…</p>
{/if}

<style>
	.crumb {
		margin: 0;
		font-size: var(--text-sm);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: baseline;
		margin-bottom: var(--space-3);
	}
	.head h1 {
		margin: 0;
	}
</style>
