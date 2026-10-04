<!-- The section links with icons (sidebar and mobile menu). `compact` hides the labels visually. -->
<script lang="ts">
	import { page } from '$app/state';
	import { inSection, sections } from '../nav.ts';
	import Icon from '../ui/Icon.svelte';

	let { label, compact = false }: { label: string; compact?: boolean } = $props();
</script>

<nav aria-label={label}>
	<ul class={{ compact }}>
		{#each sections as s (s.href)}
			<li>
				<a href={s.href} aria-current={inSection(page.url.pathname, s) ? 'page' : undefined} title={compact ? s.label : undefined}>
					<Icon d={s.icon} />
					<span class={{ 'visually-hidden': compact }}>{s.label}</span>
				</a>
			</li>
		{/each}
	</ul>
</nav>

<style>
	ul {
		display: flex;
		flex-direction: column;
		gap: 2px;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	a {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-height: 2.625rem;
		padding: 0 var(--space-3);
		font-weight: 500;
		color: var(--color-text-muted);
		text-decoration: none;
		border-radius: var(--radius-md);
	}
	.compact a {
		justify-content: center;
		padding: 0;
	}
	a:hover {
		color: var(--color-text);
		background: var(--color-surface-2);
	}
	a[aria-current='page'] {
		font-weight: 600;
		color: var(--color-text);
		background: var(--color-surface-2);
	}
	a[aria-current='page'] :global(.icon) {
		color: var(--color-accent);
	}
</style>
