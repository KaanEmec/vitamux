<!-- Loading placeholder with an accessible label; `variant` sets its size (line, block, chart). -->
<script lang="ts">
	let { variant = 'line', label = 'Loading…' }: { variant?: 'line' | 'block' | 'chart'; label?: string } = $props();
</script>

<div class={['skeleton', variant]} role="progressbar" aria-label={label}></div>

<style>
	.skeleton {
		background: var(--color-surface-2);
		border-radius: var(--radius-sm);
	}
	.line {
		height: 1rem;
		max-width: 20rem;
	}
	.block {
		height: 6rem;
	}
	.chart {
		height: 17.5rem;
		border-radius: var(--radius-lg);
	}
	@media (prefers-reduced-motion: no-preference) {
		.skeleton {
			animation: pulse 1.4s ease-in-out infinite;
		}
	}
	@keyframes pulse {
		50% {
			opacity: 0.55;
		}
	}
</style>
