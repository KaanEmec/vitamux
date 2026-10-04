<!-- The Vitamux mark (decorative; the adjacent text names it). -->
<script lang="ts">
	let { size = 28 }: { size?: number } = $props();
</script>

<svg class="logo" width={size} height={size} viewBox="0 0 28 28" aria-hidden="true">
	<rect width="28" height="28" rx="8" />
	<path d="M5 15h4l2.5-6 4 11 2.5-5H23" />
</svg>

<style>
	.logo {
		flex: none;
	}
	rect {
		fill: var(--color-accent);
	}
	path {
		fill: none;
		stroke: var(--color-on-accent);
		stroke-width: 2.2;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
</style>
