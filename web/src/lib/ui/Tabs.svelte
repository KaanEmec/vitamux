<!-- Section tabs as links (each tab has its own URL); the current one has aria-current="page". -->
<script lang="ts">
	let { label, items }: { label: string; items: { href: string; label: string; current: boolean }[] } = $props();
</script>

<nav class="tabs" aria-label={label}>
	<ul>
		{#each items as t (t.href)}
			<li><a href={t.href} aria-current={t.current ? 'page' : undefined}>{t.label}</a></li>
		{/each}
	</ul>
</nav>

<style>
	ul {
		display: flex;
		gap: var(--space-1);
		margin: 0 0 var(--space-5);
		padding: 0;
		overflow-x: auto;
		list-style: none;
		border-bottom: 1px solid var(--color-border);
		scrollbar-width: none;
	}
	a {
		display: block;
		padding: var(--space-2) var(--space-3);
		font-weight: 500;
		color: var(--color-text-muted);
		white-space: nowrap;
		text-decoration: none;
		border-bottom: 2px solid transparent;
	}
	a:hover {
		color: var(--color-text);
	}
	a[aria-current='page'] {
		font-weight: 600;
		color: var(--color-text);
		border-bottom-color: var(--color-accent);
	}
</style>
