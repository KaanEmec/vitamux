<!--
	Settings section: one h1 and links to its pages. The pages set their own <title> and an h2.
	The shared form and table styles below apply to every page in the section.
-->
<script lang="ts">
	import { page } from '$app/state';

	let { children } = $props();

	const tabs = [
		{ href: '/settings', label: 'Profile' },
		{ href: '/settings/devices', label: 'Devices' },
		{ href: '/settings/api-keys', label: 'API keys' },
		{ href: '/settings/ai', label: 'AI providers' },
		{ href: '/settings/retention', label: 'Retention' },
		{ href: '/settings/backups', label: 'Backups and export' },
		{ href: '/settings/security', label: 'Security' },
		{ href: '/settings/system', label: 'System' }
	];
	const current = (href: string) => page.url.pathname.replace(/\/$/, '') === href;
</script>

<h1>Settings</h1>
<nav class="tabs" aria-label="Settings pages">
	{#each tabs as t (t.href)}
		<a href={t.href} aria-current={current(t.href) ? 'page' : undefined}>{t.label}</a>
	{/each}
</nav>

<div class="settings">
	{@render children()}
</div>

<style>
	.tabs {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin-bottom: var(--space-5);
		border-bottom: 1px solid var(--color-border);
	}
	.tabs a {
		padding: var(--space-2) var(--space-4);
		color: var(--color-text);
		text-decoration: none;
		border-bottom: 3px solid transparent;
	}
	.tabs a:hover {
		background: var(--color-surface-2);
	}
	.tabs a[aria-current='page'] {
		font-weight: 600;
		color: var(--color-accent);
		border-bottom-color: var(--color-accent);
	}

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
	.settings :global(.field select),
	.settings :global(.field textarea) {
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
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
