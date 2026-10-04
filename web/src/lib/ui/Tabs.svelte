<!-- Section tabs as links (each tab has its own URL); the current one has aria-current="page" (base.css .tabs). -->
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
