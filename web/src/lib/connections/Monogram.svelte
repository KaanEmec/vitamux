<!-- A source's initial on a tile in its stable colour (the name always sits next to it). -->
<script lang="ts">
	import { sourceClass } from '../ui/source.ts';
	import { providerLabel } from './connections.ts';

	let { provider, large = false }: { provider: string; large?: boolean } = $props();
</script>

<span class={['mono', sourceClass(provider), large && 'large']} aria-hidden="true">{providerLabel(provider).charAt(0)}</span>

<style>
	.mono {
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		width: 2.5rem;
		height: 2.5rem;
		font-size: var(--text-lg);
		font-weight: 700;
		background: color-mix(in srgb, var(--src) 18%, var(--color-surface));
		border: 1px solid color-mix(in srgb, var(--src) 55%, transparent);
		border-radius: var(--radius-md);
	}
	.large {
		width: 3rem;
		height: 3rem;
		font-size: var(--text-xl);
	}
</style>
