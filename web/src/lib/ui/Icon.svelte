<!-- A stroke icon from icons.ts. Decorative unless `label` is given. -->
<script lang="ts">
	let { d, size = 18, label = '' }: { d: string; size?: number; label?: string } = $props();
</script>

<svg
	class="icon"
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width="1.8"
	stroke-linecap="round"
	stroke-linejoin="round"
	role={label ? 'img' : undefined}
	aria-label={label || undefined}
	aria-hidden={label ? undefined : 'true'}
	focusable="false"><path {d} /></svg
>

<style>
	.icon {
		flex: none;
	}
</style>
