<!--
	Data section: one h1 and links to the daily view, sleep comparison and workouts. The
	pages below set their own <title> and an h2.
-->
<script lang="ts">
	import { page } from '$app/state';

	let { children } = $props();

	const tabs = [
		{ href: '/data', label: 'Daily values' },
		{ href: '/data/sleep', label: 'Sleep' },
		{ href: '/data/workouts', label: 'Workouts' }
	];
	// Drilldowns (/data/day/…) belong to the daily view.
	const current = (href: string) =>
		href === '/data' ? page.url.pathname === '/data' || page.url.pathname.startsWith('/data/day/') : page.url.pathname === href;
</script>

<h1>Data</h1>
<nav class="tabs" aria-label="Data views">
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
