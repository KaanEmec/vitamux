<!--
	Settings section: one h1 and links to its pages. The pages set their own <title> and an h2.
	The shared form and table styles below apply to every page in the section.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { settingsPages } from '#lib/nav.ts';
	import Tabs from '#lib/ui/Tabs.svelte';

	let { children } = $props();

	const tabs = $derived(settingsPages.map((p) => ({ ...p, current: page.url.pathname.replace(/\/$/, '') === p.href })));
</script>

<h1>Settings</h1>
<Tabs label="Settings pages" items={tabs} />

<div class="settings">
	{@render children()}
</div>

<style>
	.settings :global(section) {
		margin-bottom: var(--space-6);
	}
	.settings :global(table) {
		width: 100%;
		border-collapse: collapse;
		margin-bottom: var(--space-4);
	}
	.settings :global(th),
	.settings :global(td) {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--color-border);
	}
	.settings :global(thead th) {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
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
		background: var(--color-surface-2);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
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
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
</style>
