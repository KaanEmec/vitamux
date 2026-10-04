<!--
	Source order (setting sources.priority): the owner's providers, first preferred. Metrics without
	a built-in rule resolve through the default rule, which tries these providers first
	(docs/resolution-defaults.md#default-rule). Lists the saved order, then connected providers not in it.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { api, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { providerLabel } from '#lib/connections/connections.ts';
	import { loadProviders } from '#lib/connections/providers.svelte.ts';
	import { loadSettings, patchSettings } from '#lib/settings/api.ts';
	import Card from '#lib/settings/Card.svelte';
	import Notice from '#lib/settings/Notice.svelte';

	const key = 'sources.priority';
	const uid = $props.id();

	let order = $state<string[] | null>(null);
	let problem = $state<Problem | null>(null);
	let saved = $state(false);
	let busy = $state(false);

	onMount(async () => {
		void loadProviders();
		const [settings, conns] = await Promise.all([loadSettings(), api.GET('/api/v1/connections')]);
		problem = settings.problem ?? conns.error ?? null;
		const list = [...((settings.settings?.[key] as string[] | undefined) ?? [])];
		// Manual entries are always the last step of the default rule, so they are not ordered here.
		for (const c of conns.data?.connections ?? []) if (c.provider !== 'manual' && !list.includes(c.provider)) list.push(c.provider);
		order = list;
	});

	async function move(i: number, by: -1 | 1) {
		if (!order) return;
		const code = order[i];
		[order[i], order[i + by]] = [order[i + by], order[i]];
		saved = false;
		// Keep focus on the moved provider; at an end, its other button.
		await tick();
		const there = i + by === 0 || i + by === order.length - 1;
		const dir = by < 0 ? (there ? 'down' : 'up') : there ? 'up' : 'down';
		document.getElementById(`${uid}-${code}-${dir}`)?.focus();
	}

	async function save() {
		if (!order) return;
		busy = true;
		problem = null;
		saved = false;
		const res = await patchSettings({ [key]: order });
		busy = false;
		problem = res.problem;
		saved = !res.problem;
	}
</script>

<Card
	title="Source order"
	id="source-order"
	description="Metrics without a built-in rule take the first of these sources that has a value, then a watch, band, ring, chest strap, arm band, phone, other devices and manual entries. Built-in and your own rules are not affected."
>
	<ProblemAlert {problem} fields={[key]} />
	{#if saved}<Notice>Saved.</Notice>{/if}
	{#if order === null}
		<p class="muted" role="status">Loading sources…</p>
	{:else if order.length === 0}
		<p class="muted">No sources connected yet. Connect one under <a href="/connections">Connections</a>.</p>
	{:else}
		<ol class="order" aria-label="Source order, first preferred">
			{#each order as code, i (code)}
				<li>
					<span class="rank muted">{i + 1}</span>
					<span class="name">{providerLabel(code)}</span>
					<button id="{uid}-{code}-up" class="btn sm" type="button" disabled={i === 0} aria-label="Move {providerLabel(code)} up" onclick={() => move(i, -1)}>Up</button>
					<button id="{uid}-{code}-down" class="btn sm" type="button" disabled={i === order.length - 1} aria-label="Move {providerLabel(code)} down" onclick={() => move(i, 1)}>Down</button>
				</li>
			{/each}
		</ol>
		<button class="btn primary" type="button" disabled={busy} onclick={save}>Save order</button>
	{/if}
</Card>

<style>
	.order {
		display: grid;
		gap: var(--space-2);
		max-width: 28rem;
		margin: 0 0 var(--space-4);
		padding: 0;
		list-style: none;
	}
	li {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	.rank {
		min-width: 1.5rem;
		font-variant-numeric: tabular-nums;
	}
	.name {
		flex: 1;
	}
</style>
