<!-- Loading placeholder with an accessible label; `variant` sets its size (line, block, chart). -->
<script lang="ts">
	let { variant = 'line', label = 'Loading…' }: { variant?: 'line' | 'block' | 'chart'; label?: string } = $props();
</script>

<div class={['skeleton', variant]} role="progressbar" aria-label={label}></div>

<style>
	.skeleton {
		background: linear-gradient(90deg, var(--color-surface-2) 25%, var(--color-selected) 50%, var(--color-surface-2) 75%) 0 0 / 200% 100%;
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
			animation: shimmer 1.6s ease-in-out infinite;
		}
	}
	@keyframes shimmer {
		from {
			background-position: 100% 0;
		}
		to {
			background-position: -100% 0;
		}
	}
</style>
