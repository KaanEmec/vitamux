<!--
	Settings section: one h1, the pages grouped in a side menu (a scrolling row on narrow screens),
	the current page's h2, and the page itself: a short intro and a stack of cards
	(lib/settings/Card.svelte). Pages set their own <title>. The form, table and notice styles
	below apply to every card in the section.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { settingsPages } from '#lib/nav.ts';

	let { children } = $props();

	const groups = [...new Set(settingsPages.map((p) => p.group))].map((name) => ({ name, items: settingsPages.filter((p) => p.group === name) }));
	const path = $derived(page.url.pathname.replace(/\/$/, '') || '/');
	const current = $derived(settingsPages.find((p) => p.href === path));
</script>

<h1>Settings</h1>

<div class="layout">
	<nav class="menu" aria-label="Settings pages">
		{#each groups as g (g.name)}
			<div class="group">
				<p class="group-name" id="settings-group-{g.name}">{g.name}</p>
				<ul aria-labelledby="settings-group-{g.name}">
					{#each g.items as p (p.href)}
						<li><a href={p.href} aria-current={p.href === path ? 'page' : undefined}>{p.label}</a></li>
					{/each}
				</ul>
			</div>
		{/each}
	</nav>

	<div class="settings">
		{#if current}<h2 class="page-title">{current.label}</h2>{/if}
		{@render children()}
	</div>
</div>

<style>
	.layout {
		display: grid;
		grid-template-columns: 13rem minmax(0, 1fr);
		gap: var(--space-6);
		align-items: start;
	}

	.menu {
		display: grid;
		gap: var(--space-4);
	}
	.group-name {
		margin: 0 0 var(--space-1);
		padding: 0 var(--space-3);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--color-text-faint);
	}
	.menu ul {
		display: grid;
		gap: 2px;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.menu a {
		display: block;
		padding: var(--space-2) var(--space-3);
		font-weight: 500;
		color: var(--color-text-muted);
		text-decoration: none;
		border-radius: var(--radius-sm);
	}
	.menu a:hover {
		color: var(--color-text);
		background: var(--color-surface-2);
	}
	.menu a[aria-current='page'] {
		font-weight: 600;
		color: var(--color-text);
		background: var(--color-selected);
	}

	@media (max-width: 48rem) {
		.layout {
			grid-template-columns: minmax(0, 1fr);
			gap: var(--space-4);
		}
		.menu {
			display: flex;
			gap: var(--space-4);
			padding-bottom: var(--space-1);
			overflow-x: auto;
			scrollbar-width: none;
		}
		.group {
			display: flex;
			flex: none;
			gap: var(--space-1);
			align-items: center;
		}
		.group-name {
			padding: 0 var(--space-1);
		}
		.menu ul {
			display: flex;
		}
		.menu a {
			white-space: nowrap;
		}
	}

	.settings {
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	.page-title {
		margin: 0;
		font-size: var(--text-xl);
		letter-spacing: var(--tracking-tight);
	}
	.settings :global(.lede) {
		max-width: 46rem;
		margin: calc(-1 * var(--space-2)) 0 0;
		color: var(--color-text-muted);
	}
	.settings :global(.table-wrap) {
		overflow-x: auto;
	}
	.settings :global(table) {
		width: 100%;
		border-collapse: collapse;
	}
	.settings :global(th),
	.settings :global(td) {
		padding: var(--space-3);
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--color-border);
	}
	.settings :global(thead th) {
		font-size: var(--text-xs);
		font-weight: 600;
		color: var(--color-text-muted);
	}
	.settings :global(tbody tr:last-child > *) {
		border-bottom: 0;
	}
	.settings :global(.actions) {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
	}
	.settings :global(.check) {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		margin-bottom: var(--space-3);
	}
	.settings :global(.check input) {
		margin-top: 0.35em;
	}
	.settings :global(.check .hint) {
		display: block;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.settings :global(.secret) {
		display: block;
		padding: var(--space-3);
		margin: var(--space-3) 0;
		font-family: var(--font-mono);
		overflow-wrap: anywhere;
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.settings :global(.callout) {
		padding: var(--space-4);
		margin-bottom: var(--space-4);
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.settings :global(.callout:last-child) {
		margin-bottom: 0;
	}
	.settings :global(.row-form) {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-3);
		align-items: flex-end;
	}
	.settings :global(.row-form .field) {
		margin-bottom: 0;
	}
	.settings :global(fieldset) {
		margin: 0 0 var(--space-4);
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.settings :global(legend) {
		padding: 0 var(--space-2);
		font-size: var(--text-sm);
		font-weight: 500;
	}
</style>
