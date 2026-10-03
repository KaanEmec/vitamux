<!--
	Lab results section: one h1 and links to the documents (upload and review) and to the
	confirmed results. Pages below set their own <title> and h2.
-->
<script lang="ts">
	import { page } from '$app/state';

	let { children } = $props();

	const tabs = [
		{ href: '/lab', label: 'Documents' },
		{ href: '/lab/results', label: 'Results' }
	];
	// Review pages (/lab/documents/…) belong to the documents tab.
	const current = (href: string) =>
		href === '/lab' ? page.url.pathname === '/lab' || page.url.pathname.startsWith('/lab/documents/') : page.url.pathname === href;
</script>

<h1>Lab results</h1>
<nav class="tabs" aria-label="Lab views">
	{#each tabs as t (t.href)}
		<a href={t.href} aria-current={current(t.href) ? 'page' : undefined}>{t.label}</a>
	{/each}
</nav>

{@render children()}

<style>
	.tabs {
		display: flex;
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
</style>
