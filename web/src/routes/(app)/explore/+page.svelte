<!--
	Explore: everything Vitamux has stored (GET /inventory), grouped by section. Each row shows a
	30-day sparkline (GET /resolved/summary, metrics only), the latest value, its sources, days
	with data and the last record, and opens its view (lib/explore/links.ts). Filters: text,
	provider, device and origin; catalogue metrics without data can be listed too (GET /metrics).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { providerLabel } from '#lib/connections/connections.ts';
	import { deviceKey, deviceLabel, itemName, lastSeen, latestValue, matches, originLabel, sectionOf, type Item } from '#lib/explore/inventory.ts';
	import { exploreHref, pinKey } from '#lib/explore/links.ts';
	import { Pins } from '#lib/explore/pins.svelte.ts';
	import Button from '#lib/ui/Button.svelte';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';

	const sparkline = import('#lib/charts/Sparkline.svelte');
	const summaryBatch = 20; // GET /resolved/summary takes at most 20 metrics

	let items = $state<Item[] | null>(null);
	let pending = $state(false);
	let problem = $state<Problem | null>(null);
	let catalogue = $state<Schemas['Metric'][]>([]);
	let sparks = $state<Record<string, (number | null)[]>>({});
	const pins = new Pins();

	let text = $state('');
	let provider = $state('');
	let device = $state('');
	let origin = $state('');
	let showEmpty = $state(false);
	let collapsed = $state<Record<string, boolean>>({});

	onMount(async () => {
		void pins.load();
		const res = await api.GET('/api/v1/inventory');
		problem = res.error ?? null;
		if (!res.data) return;
		items = res.data.items;
		pending = res.data.aggregates_pending;
		const codes = items.filter((i) => i.kind === 'metric').map((i) => i.code);
		for (let i = 0; i < codes.length; i += summaryBatch) void loadSparks(codes.slice(i, i + summaryBatch));
	});

	async function loadSparks(metrics: string[]) {
		const res = await api.GET('/api/v1/resolved/summary', { params: { query: { metrics } } });
		for (const [code, s] of Object.entries(res.data?.metrics ?? {})) {
			sparks[code] = s.sparkline.map((p) => (typeof p.value === 'number' ? p.value : null));
		}
	}

	$effect(() => {
		if (showEmpty && !catalogue.length) {
			void api.GET('/api/v1/metrics').then((res) => (catalogue = res.data?.metrics ?? []));
		}
	});

	/** Catalogue metrics with no data, as empty items. */
	const empty = $derived.by((): Item[] => {
		if (!showEmpty || !items) return [];
		const have = new Set(items.filter((i) => i.kind === 'metric').map((i) => i.code));
		return catalogue
			.filter((m) => !have.has(m.code))
			.map((m) => ({ kind: 'metric', code: m.code, metric: m, count: 0, days: 0, first_date: '', last_date: '', providers: [], devices: [], origins: [] }));
	});

	const all = $derived([...(items ?? []), ...empty]);
	const byMetric = $derived(new Map(all.filter((i) => i.kind === 'metric').map((i) => [i.code, i])));
	const providers = $derived([...new Set(all.flatMap((i) => i.providers))].sort());
	const devices = $derived([...new Map(all.flatMap((i) => i.devices).map((d) => [deviceKey(d), d])).values()]);
	const origins = $derived([...new Map(all.flatMap((i) => i.origins).map((o) => [o.key, o])).values()]);

	const sections = $derived.by(() => {
		const out: Record<string, Item[]> = {};
		for (const it of all) {
			if (matches(it, { text, provider, device, origin })) (out[sectionOf(it, byMetric)] ??= []).push(it);
		}
		// Events and lab analytes after the catalogue sections.
		const last = ['Events', 'Lab analytes'];
		return Object.entries(out).sort(([a], [b]) => last.indexOf(a) - last.indexOf(b));
	});

	const counts = $derived.by(() => {
		const n = (k: Item['kind']) => (items ?? []).filter((i) => i.kind === k).length;
		const parts = [
			[n('metric'), 'metric', 'metrics'],
			[n('event'), 'event type', 'event types'],
			[n('analyte'), 'lab analyte', 'lab analytes']
		] as const;
		return parts.filter(([c]) => c > 0).map(([c, one, many]) => `${c} ${c === 1 ? one : many}`);
	});

	const filtered = $derived(!!(text || provider || device || origin));

	function clear() {
		text = provider = device = origin = '';
	}
</script>

<svelte:head><title>Explore · Vitamux</title></svelte:head>

<header class="head">
	<h1>Explore</h1>
	<p class="muted">Everything Vitamux has stored, grouped by kind{counts.length ? ` · ${counts.join(' · ')}` : ''}.</p>
</header>

<ProblemAlert {problem} />
<ProblemAlert problem={pins.problem} />
{#if pending}<p class="note muted">Counts are catching up while the hourly aggregates are rebuilt.</p>{/if}

<div class="filters">
	<label class="search">
		<Icon d={icons.search} size={16} />
		<span class="visually-hidden">Search</span>
		<input type="search" bind:value={text} placeholder="Search metric, code, device or analyte" />
	</label>
	{#if providers.length > 1}
		<div class="chips" role="group" aria-label="Source">
			<button type="button" class="filter" aria-pressed={!provider} onclick={() => (provider = '')}>All sources</button>
			{#each providers as p (p)}
				<button type="button" class="filter" aria-pressed={provider === p} onclick={() => (provider = provider === p ? '' : p)}>
					<Chip source={p}>{providerLabel(p)}</Chip>
				</button>
			{/each}
		</div>
	{/if}
	{#if devices.length > 1}
		<label class="select">
			<span class="visually-hidden">Device</span>
			<select bind:value={device}>
				<option value="">All devices</option>
				{#each devices as d (deviceKey(d))}<option value={deviceKey(d)}>{deviceLabel(d)}</option>{/each}
			</select>
		</label>
	{/if}
	{#if origins.length > 1}
		<label class="select">
			<span class="visually-hidden">Origin app</span>
			<select bind:value={origin}>
				<option value="">All origin apps</option>
				{#each origins as o (o.key)}<option value={o.key}>{originLabel(o)}</option>{/each}
			</select>
		</label>
	{/if}
	<label class="check"><input type="checkbox" bind:checked={showEmpty} />Show catalogue items with no data</label>
</div>

{#if !items && !problem}
	<Skeleton variant="block" label="Loading the inventory" />
{:else if items && !items.length && !showEmpty}
	<EmptyState icon={icons.explore} title="Nothing stored yet" text="Connect a source and its data shows up here after the first sync.">
		<Button href="/connections">Connections</Button>
	</EmptyState>
{:else if items && !sections.length}
	<EmptyState icon={icons.search} title="Nothing matches these filters">
		{#if filtered}<Button onclick={clear}>Clear filters</Button>{/if}
	</EmptyState>
{/if}

{#each sections as [name, rows] (name)}
	{@const open = !collapsed[name]}
	<section class="section card" aria-label={name}>
		<div class="section-head">
			<h2>
				<button type="button" aria-expanded={open} onclick={() => (collapsed[name] = open)}>
					{name}
					<svg class={['caret', !open && 'closed']} width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 15l6-6 6 6" /></svg>
				</button>
			</h2>
			<span class="muted">{rows.length} {rows.length === 1 ? 'item' : 'items'}</span>
		</div>
		{#if open}
			<div class="table-wrap">
				<table>
					<thead>
						<tr>
							<th scope="col" class="pin"><span class="visually-hidden">Pinned</span></th>
							<th scope="col">Name</th>
							<th scope="col" class="wide spark">Last 30 days</th>
							<th scope="col" class="num latest">Latest</th>
							<th scope="col" class="wide sources-col">Sources</th>
							<th scope="col" class="num wide days">Days with data</th>
							<th scope="col" class="num last">Last record</th>
						</tr>
					</thead>
					<tbody>
						{#each rows as it (it.kind + it.code)}
							{@const key = pinKey(it)}
							{@const latest = latestValue(it)}
							{@const name = itemName(it)}
							<tr class:empty={!it.days}>
								<td class="pin">
									{#if key && pins.layout}
										<button
											type="button"
											class={['star', pins.has(key) && 'pinned']}
											aria-pressed={pins.has(key)}
											aria-label="Pin {name} to the dashboard"
											onclick={() => pins.toggle(key)}
										>
											<Icon d={icons.star} size={16} />
										</button>
									{/if}
								</td>
								<td>
									<a class="name" href={exploreHref(it)}>{name}</a>
									<div class="code">{it.code}</div>
								</td>
								<td class="wide spark">
									{#if sparks[it.code]?.some((v) => v != null)}
										{#await sparkline then { default: Sparkline }}
											<Sparkline ys={sparks[it.code]} bars={it.metric?.aggregation === 'additive'} />
										{/await}
									{/if}
								</td>
								<td class="num nowrap"><strong>{latest.value}</strong> <span class="muted unit">{latest.unit}</span></td>
								<td class="wide">
									<div class="sources">
										{#each it.providers as p (p)}<Chip source={p}>{providerLabel(p)}</Chip>{/each}
									</div>
								</td>
								<td class="num wide">{it.days.toLocaleString()}</td>
								<td class="num nowrap muted">{lastSeen(it)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
{/each}

{#if showEmpty && !catalogue.length}<p class="muted">Loading the catalogue…</p>{/if}
<p class="visually-hidden" aria-live="polite">{filtered ? `${sections.reduce((n, [, r]) => n + r.length, 0)} items match` : ''}</p>

<style>
	.head p {
		margin: 0;
	}
	.note {
		font-size: var(--text-sm);
	}
	.filters {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin: var(--space-5) 0;
	}
	.search {
		display: flex;
		flex: 1 1 16rem;
		align-items: center;
		gap: var(--space-2);
		max-width: 24rem;
		min-height: var(--control-h);
		padding: 0 var(--space-3);
		color: var(--color-text-muted);
		background: var(--color-surface);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-md);
	}
	.search:focus-within {
		outline: 2px solid var(--color-focus);
		outline-offset: 2px;
	}
	.search input {
		flex: 1;
		min-width: 0;
		font: inherit;
		color: var(--color-text);
		background: transparent;
		border: 0;
		outline: none;
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.filter {
		display: inline-flex;
		align-items: center;
		min-height: 2.25rem;
		padding: 0 var(--space-1);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
		background: transparent;
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-pill);
		cursor: pointer;
	}
	.filter:first-child {
		padding: 0 var(--space-3);
	}
	.filter[aria-pressed='true'] {
		color: var(--color-text);
		background: var(--color-selected);
		border-color: var(--color-accent);
	}
	.filter :global(.chip) {
		background: transparent;
	}
	select {
		min-height: 2.25rem;
		padding: 0 var(--space-3);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-md);
	}
	.check {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-height: var(--control-h);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.section {
		padding: 0;
		margin-bottom: var(--space-4);
		overflow: hidden;
	}
	.section-head {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--color-border);
		font-size: var(--text-sm);
	}
	.section-head h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	.section-head button {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-height: 2.25rem;
		padding: 0;
		font: inherit;
		color: inherit;
		background: none;
		border: 0;
		cursor: pointer;
	}
	.caret {
		fill: none;
		stroke: var(--color-text-muted);
		stroke-width: 2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.caret.closed {
		transform: rotate(180deg);
	}
	.table-wrap {
		overflow-x: auto;
	}
	/* Fixed columns line up across the sections. */
	table {
		width: 100%;
		min-width: 0;
		table-layout: fixed;
		border-collapse: collapse;
		font-size: var(--text-sm);
	}
	th {
		padding: var(--space-2) var(--space-3);
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--color-text-muted);
		text-align: left;
	}
	td {
		padding: var(--space-2) var(--space-3);
		border-top: 1px solid var(--color-border);
		vertical-align: middle;
	}
	.num {
		text-align: right;
	}
	.nowrap {
		white-space: nowrap;
	}
	.pin {
		width: 2.75rem;
		padding-right: 0;
	}
	.star {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 2rem;
		height: 2rem;
		color: var(--color-text-faint);
		background: transparent;
		border: 0;
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.star.pinned {
		color: var(--color-accent);
	}
	.star.pinned :global(path) {
		fill: currentColor;
	}
	.name {
		font-weight: 500;
		color: var(--color-text);
		text-decoration: none;
	}
	.name:hover {
		text-decoration: underline;
	}
	.code {
		font-family: var(--font-mono);
		font-size: var(--text-2xs);
		color: var(--color-text-faint);
	}
	.spark {
		width: 10rem;
	}
	.latest {
		width: 9rem;
	}
	.sources-col {
		width: 30%;
	}
	.days {
		width: 8rem;
	}
	.last {
		width: 8.5rem;
	}
	td {
		overflow-wrap: anywhere;
	}
	.unit {
		font-size: var(--text-xs);
	}
	.sources {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
	}
	tr.empty .name {
		color: var(--color-text-muted);
	}
	@media (max-width: 48rem) {
		.wide {
			display: none;
		}
		.latest {
			width: 6.5rem;
		}
		.last {
			width: 5rem;
		}
		.nowrap {
			white-space: normal;
		}
	}
</style>
