<!--
	Settings › Devices › Sources (J22.25): the Apple Health source filter of one paired iPhone. One
	row per app the phone found in Apple Health: what it writes, its origin classification (as in
	Settings › Devices), Take / Ignore or some types, why it is ignored by default, records held raw
	on the server, and a link to Explore filtered to it. Every change PUTs the full list of explicit
	choices with the version it was read at (409: changed elsewhere, reloaded).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import { typeLabel } from '#lib/devices/format.ts';
	import { appName, changeNotice, modeText, withChoice, type FilterOrigin, type Mode, type Next } from '#lib/devices/sourceFilter.ts';
	import TypesDialog from '#lib/devices/TypesDialog.svelte';
	import { ago } from '#lib/settings/format.ts';
	import Card from '#lib/settings/Card.svelte';
	import Notice from '#lib/settings/Notice.svelte';
	import Badge from '#lib/ui/Badge.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Segmented from '#lib/ui/Segmented.svelte';

	const id = $derived(page.params.id ?? '');
	const uid = $props.id();

	let view = $state<Schemas['SourceFilterView'] | null>(null);
	let device = $state<Schemas['PairedDevice'] | null>(null);
	let targets = $state<Schemas['RelayTarget'][]>([]);
	let problem = $state<Problem | null>(null);
	let conflict = $state(false);
	let notice = $state('');
	let busy = $state(false);
	let choosing = $state<FilterOrigin | null>(null);
	/** Bumped after every save, so the Take / Ignore controls show the server's state again. */
	let gen = $state(0);

	async function load() {
		const res = await api.GET('/api/v1/devices/{id}/source-filter', { params: { path: { id } } });
		if (res.error) problem = res.error;
		else view = res.data;
	}

	onMount(() => {
		void load();
		void api.GET('/api/v1/devices').then((res) => (device = res.data?.devices.find((d) => d.id === id) ?? null));
		void api.GET('/api/v1/origins').then((res) => (targets = res.data?.relay_targets ?? []));
	});

	async function save(o: FilterOrigin, next: Next | null) {
		if (!view) return;
		problem = null;
		conflict = false;
		notice = '';
		busy = true;
		const res = await api.PUT('/api/v1/devices/{id}/source-filter', {
			params: { path: { id } },
			body: { version: view.version, origins: withChoice(view.origins, o.bundle_id, next) }
		});
		busy = false;
		gen++;
		if (res.error) {
			problem = res.error;
			if (res.error.status === 409) {
				conflict = true;
				await load();
			}
			return;
		}
		view = res.data;
		const after = view.origins.find((x) => x.bundle_id === o.bundle_id);
		if (after) notice = changeNotice(o, after);
	}

	function saveTypes(next: Next) {
		const o = choosing;
		choosing = null;
		if (o) void save(o, next);
	}

	function setMode(o: FilterOrigin, mode: Mode) {
		if (mode !== o.mode) void save(o, { mode });
	}

	const name = $derived(device?.name ?? 'this device');
	const targetName = (code: string) => targets.find((t) => t.code === code)?.name ?? code;
	const initial = (o: FilterOrigin) => appName(o).trim().charAt(0).toUpperCase();
	const lastSample = (iso: string | undefined) => (iso ? ago(iso) : '');
</script>

<svelte:head><title>Sources of {name} · Vitamux</title></svelte:head>

<nav class="back" aria-label="Breadcrumb"><a href="/settings/devices">← Devices</a></nav>
<h2 class="title">Sources of {name}</h2>
<p class="lede">
	Choose which apps’ data this phone takes from Apple Health. Data from an ignored app stays on the phone, so a provider you connect
	directly is not collected a second time.
</p>

<Card
	title="Apps in Apple Health"
	id="apps"
	description={view
		? view.sources_reported_at
			? `Apps reported by the phone ${ago(view.sources_reported_at)}.`
			: 'The phone has not reported its apps yet.'
		: ''}
>
	{#snippet aside()}
		<button class="btn sm" type="button" onclick={load}>Refresh</button>
	{/snippet}
	{#if notice}<Notice>{notice}</Notice>{/if}
	<ProblemAlert {problem} lead={conflict ? 'The source filter changed elsewhere, so it was reloaded. Check it and choose again.' : ''} />
	{#if view === null}
		{#if !problem}<p class="muted" role="status">Loading sources…</p>{/if}
	{:else if view.origins.length === 0}
		<EmptyState title="No apps yet" text="The phone lists the apps that write to Apple Health on its next sync." />
	{:else}
		<ul class="apps">
			{#each view.origins as o, i (o.bundle_id)}
				{@const app = appName(o)}
				<li>
					<article class="app" aria-labelledby="{uid}-{i}">
						<span class="tile" aria-hidden="true">{initial(o)}</span>
						<div class="about">
							<p class="name-line">
								<strong id="{uid}-{i}">{app}</strong>
								{#if o.explicit}<Badge tone="accent">Your choice</Badge>{:else}<Badge>Default</Badge>{/if}
							</p>
							{#if o.name}<code class="muted bundle">{o.bundle_id}</code>{/if}
							<p class="line">
								{#if o.classification === 'native'}<StatusIcon status="ok" /> Native
								{:else if o.classification === 'relayed'}<StatusIcon status="info" /> Relayed from {targetName(o.relayed_provider ?? '')}
								{:else}<StatusIcon status="off" /> Direct{/if}
								<span class="muted">· {modeText(o)}</span>
							</p>
							{#if o.writes.length}
								<ul class="writes" aria-label="What {app} writes">
									{#each o.writes as w (w.type)}
										<li title={w.type}>{typeLabel(w.type)}{#if w.last_sample_at}&nbsp;<span class="muted">· {lastSample(w.last_sample_at)}</span>{/if}</li>
									{/each}
								</ul>
							{:else}
								<p class="line muted">Not reported yet</p>
							{/if}
							{#if o.default_mode === 'ignore' && o.default_reason === 'direct_connection'}
								<p class="why">
									<StatusIcon status="info" />
									<span>You get {o.reason_provider_name ?? o.reason_provider ?? 'this provider'} directly; its copy in Apple Health would count twice.</span>
								</p>
							{/if}
							{#if o.ignored_records > 0}
								<p class="line muted">{o.ignored_records.toLocaleString()} {o.ignored_records === 1 ? 'record' : 'records'} held raw, not used</p>
							{/if}
						</div>
						<div class="controls">
							{#key gen}
								<Segmented
									label="{app}: take or ignore"
									options={[
										{ value: 'take', label: 'Take' },
										{ value: 'ignore', label: 'Ignore' }
									]}
									value={o.mode}
									onchange={(m: Mode) => setMode(o, m)}
								/>
							{/key}
							<div class="actions">
								<button class="btn sm" type="button" disabled={busy || o.writes.length + o.types.length === 0} onclick={() => (choosing = o)} aria-label="Choose types from {app}">
									Choose types…
								</button>
								{#if o.explicit}
									<button class="btn sm ghost" type="button" disabled={busy} onclick={() => save(o, null)} aria-label="Use default for {app}">Use default</button>
								{/if}
								<a class="btn sm ghost" href="/explore?origin={encodeURIComponent(o.bundle_id)}" aria-label="Show {app} in Explore">Show in Explore</a>
							</div>
						</div>
					</article>
				</li>
			{/each}
		</ul>
	{/if}
</Card>

{#if choosing}
	<TypesDialog origin={choosing} onclose={() => (choosing = null)} onsave={saveTypes} />
{/if}

<style>
	.back {
		font-size: var(--text-sm);
	}
	.back a {
		color: var(--color-text-muted);
		text-decoration: none;
	}
	.back a:hover {
		color: var(--color-text);
	}
	.title {
		margin: 0;
		font-size: var(--text-xl);
		letter-spacing: var(--tracking-tight);
	}
	.apps {
		display: grid;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.apps > li + li {
		border-top: 1px solid var(--color-border);
	}
	.app {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		gap: var(--space-3) var(--space-4);
		align-items: start;
		padding: var(--space-4) 0;
	}
	.tile {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: var(--tile-size-lg);
		height: var(--tile-size-lg);
		font-weight: 600;
		color: var(--color-link);
		background: var(--color-accent-soft);
		border: 1px solid color-mix(in srgb, var(--color-accent) 25%, transparent);
		border-radius: var(--radius-md);
	}
	.about {
		display: grid;
		gap: var(--space-1);
		min-width: 0;
	}
	.name-line {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin: 0;
	}
	.bundle {
		justify-self: start;
		font-size: var(--text-xs);
		overflow-wrap: anywhere;
	}
	.line {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		align-items: center;
		margin: 0;
		font-size: var(--text-sm);
	}
	.why {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr);
		gap: var(--space-1);
		align-items: start;
		margin: 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.why :global(.status-icon) {
		margin-top: 0.2em;
	}
	.writes {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		margin: 0;
		padding: 0;
		font-size: var(--text-sm);
		list-style: none;
	}
	.controls {
		display: grid;
		gap: var(--space-2);
		justify-items: end;
	}
	@media (max-width: 40rem) {
		.app {
			grid-template-columns: auto minmax(0, 1fr);
		}
		.controls {
			grid-column: 1 / -1;
			justify-items: start;
		}
	}
</style>
