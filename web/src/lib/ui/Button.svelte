<!--
	Button or link styled as a button (base.css .btn). `variant`: primary (one per view),
	secondary (default), ghost, danger. `loading` disables it and sets aria-busy.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';

	let {
		variant = 'secondary',
		size = 'md',
		href,
		loading = false,
		children,
		...rest
	}: {
		variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
		size?: 'md' | 'sm';
		href?: string;
		loading?: boolean;
		children: Snippet;
	} & HTMLButtonAttributes = $props();

	const cls = $derived(['btn', variant !== 'secondary' && variant, size === 'sm' && 'sm', rest.class]);
</script>

{#if href}
	<a class={cls} {href}>{@render children()}</a>
{:else}
	<button {...rest} class={cls} type={rest.type ?? 'button'} disabled={rest.disabled || loading} aria-busy={loading || undefined}>
		{@render children()}
	</button>
{/if}
