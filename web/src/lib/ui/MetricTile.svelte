<!-- A metric's icon tile: its icon in the metric hue on the hue's tint (lib/ui/metric.ts). Decorative. -->
<script lang="ts">
	import Icon from './Icon.svelte';
	import { icons } from './icons.ts';
	import { metricLook } from './metric.ts';

	let { code, section, size = 'md' }: { code: string; section?: string; size?: 'sm' | 'md' | 'lg' } = $props();

	const look = $derived(metricLook(code, section));
</script>

<span class={['tile', size]} style:--metric={look.color} style:--metric-tint={look.tint} aria-hidden="true">
	<Icon d={icons[look.icon]} size={size === 'lg' ? 20 : size === 'sm' ? 14 : 18} />
</span>

<style>
	.tile {
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: var(--tile-size);
		height: var(--tile-size);
		color: var(--metric);
		background: var(--metric-tint);
		border-radius: var(--radius-md);
	}
	.sm {
		width: var(--tile-size-sm);
		height: var(--tile-size-sm);
		border-radius: var(--radius-sm);
	}
	.lg {
		width: var(--tile-size-lg);
		height: var(--tile-size-lg);
	}
</style>
