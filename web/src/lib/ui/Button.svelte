<!--
	Button or link styled as a button (base.css .btn). `variant`: primary (one per view),
	secondary (default), ghost, destructive; `size`: sm, md, lg. `icon` draws an icon before the text;
	without children the button is square and needs an aria-label. `loading` disables it, sets
	aria-busy and shows a spinner.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';
	import Icon from './Icon.svelte';

	let {
		variant = 'secondary',
		size = 'md',
		href,
		icon,
		loading = false,
		children,
		...rest
	}: {
		variant?: 'primary' | 'secondary' | 'ghost' | 'destructive';
		size?: 'sm' | 'md' | 'lg';
		href?: string;
		icon?: string;
		loading?: boolean;
		children?: Snippet;
	} & HTMLButtonAttributes = $props();

	const cls = $derived(['btn', variant !== 'secondary' && variant, size !== 'md' && size, icon && !children && 'icon-btn', rest.class]);
</script>

{#snippet content()}
	{#if icon && !loading}<Icon d={icon} size={size === 'sm' ? 16 : 18} />{/if}
	{@render children?.()}
{/snippet}

{#if href}
	<a class={cls} {href} aria-label={rest['aria-label']}>{@render content()}</a>
{:else}
	<button {...rest} class={cls} type={rest.type ?? 'button'} disabled={rest.disabled || loading} aria-busy={loading || undefined}>
		{@render content()}
	</button>
{/if}
