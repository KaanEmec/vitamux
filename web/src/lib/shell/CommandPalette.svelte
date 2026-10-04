<!--
	⌘K / Ctrl+K command palette: jump to a section, a Settings page, a connection, a metric or
	its rule. A combobox over a listbox; arrows move, Enter opens, Escape closes. Mount it only
	while open (like Modal); metrics load on mount.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, type Schemas } from '../api/client.ts';
	import { providerLabel } from '../connections/connections.ts';
	import { metricLabel } from '../data/format.ts';
	import { metricHref, sections, settingsPages } from '../nav.ts';
	import Icon from '../ui/Icon.svelte';
	import { icons } from '../ui/icons.ts';

	let { connections, onclose }: { connections: Schemas['Connection'][]; onclose: () => void } = $props();

	interface Item {
		group: string;
		label: string;
		detail?: string;
		href: string;
	}

	let dialog: HTMLDialogElement;
	let query = $state('');
	let active = $state(0);
	let metrics = $state<Schemas['Metric'][]>([]);
	const id = $props.id();

	onMount(() => {
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		dialog.showModal();
		api.GET('/api/v1/metrics').then(({ data }) => {
			if (data) metrics = data.metrics;
		});
		return () => opener?.focus();
	});

	const items = $derived<Item[]>([
		...sections.map((s) => ({ group: 'Go to', label: s.label, href: s.href })),
		...settingsPages.map((s) => ({ group: 'Settings', label: s.label, href: s.href })),
		...connections.map((c) => ({ group: 'Connections', label: providerLabel(c.provider), detail: c.provider, href: `/connections/${c.id}` })),
		...metrics.map((m) => ({ group: 'Metrics', label: metricLabel(m.code), detail: m.code, href: metricHref(m.code) })),
		...metrics.map((m) => ({ group: 'Rules', label: `Rule for ${metricLabel(m.code).toLowerCase()}`, detail: m.code, href: `/rules/${encodeURIComponent(m.code)}` }))
	]);

	const matches = $derived.by(() => {
		const q = query.trim().toLowerCase();
		const hit = (i: Item) => !q || `${i.label} ${i.detail ?? ''} ${i.group}`.toLowerCase().includes(q);
		// Without a query, show the sections and settings; metrics and rules appear as you type.
		return items.filter((i) => hit(i) && (q || i.group === 'Go to' || i.group === 'Settings')).slice(0, 50);
	});

	$effect(() => {
		void matches;
		active = 0;
	});

	function open(i: Item | undefined) {
		if (!i) return;
		dialog.close();
		void goto(i.href);
	}

	function keydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
			e.preventDefault();
			const n = matches.length;
			if (n) active = (active + (e.key === 'ArrowDown' ? 1 : n - 1)) % n;
			document.getElementById(`${id}-${active}`)?.scrollIntoView({ block: 'nearest' });
		} else if (e.key === 'Enter') {
			e.preventDefault();
			open(matches[active]);
		}
	}
</script>

<dialog bind:this={dialog} aria-label="Command palette" {onclose} onclick={(e) => e.target === dialog && dialog.close()}>
	<div class="search">
		<Icon d={icons.search} size={16} />
		<input
			bind:value={query}
			onkeydown={keydown}
			role="combobox"
			aria-expanded="true"
			aria-controls="{id}-list"
			aria-activedescendant={matches.length ? `${id}-${active}` : undefined}
			aria-autocomplete="list"
			aria-label="Jump to a metric, source, rule or setting"
			placeholder="Jump to a metric, source, rule or setting"
			autocomplete="off"
			spellcheck="false"
		/>
		<kbd>Esc</kbd>
	</div>
	<ul id="{id}-list" role="listbox" aria-label="Results">
		{#each matches as m, i (m.href + m.group)}
			<!-- Options are not focusable: the combobox keeps focus (aria-activedescendant). -->
			<!-- svelte-ignore a11y_click_events_have_key_events -->
			<li id="{id}-{i}" role="option" aria-selected={i === active} onclick={() => open(m)} onpointermove={() => (active = i)}>
				<span class="group">{m.group}</span>
				<span class="label">{m.label}</span>
				{#if m.detail}<code>{m.detail}</code>{/if}
			</li>
		{:else}
			<li class="none" role="option" aria-selected="false" aria-disabled="true">No matches</li>
		{/each}
	</ul>
</dialog>

<style>
	dialog {
		width: min(36rem, calc(100vw - 2rem));
		margin: 12vh auto auto;
		padding: 0;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-2);
	}
	dialog::backdrop {
		background: var(--color-backdrop);
		backdrop-filter: blur(6px);
	}
	.search {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: 0 var(--space-4);
		color: var(--color-text-muted);
		border-bottom: 1px solid var(--color-border);
	}
	input {
		flex: 1;
		min-height: 3.25rem;
		font: inherit;
		color: var(--color-text);
		background: transparent;
		border: 0;
		outline: none;
	}
	/* The palette's search row is borderless; the global field focus glow does not apply. */
	.search input:focus {
		box-shadow: none;
	}
	kbd {
		padding: 1px var(--space-2);
		font-size: var(--text-2xs);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-xs);
	}
	ul {
		max-height: min(24rem, 60vh);
		margin: 0;
		padding: var(--space-2);
		overflow-y: auto;
		list-style: none;
	}
	li {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-height: 2.5rem;
		padding: 0 var(--space-3);
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	li[aria-selected='true'] {
		background: var(--color-selected);
	}
	.group {
		width: 6rem;
		flex: none;
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.label {
		flex: 1;
	}
	code {
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
	.none {
		color: var(--color-text-muted);
		cursor: default;
	}
</style>
