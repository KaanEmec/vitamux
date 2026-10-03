<!--
	Status shown by shape AND colour, never colour alone:
	ok = filled circle + check, warn = triangle + !, error = octagon + ×,
	info = rounded square + i, pending = dashed ring, off = hollow circle + slash.
	Pass `label` when no adjacent text names the status; otherwise it is decorative.
-->
<script lang="ts" module>
	export type Status = 'ok' | 'warn' | 'error' | 'info' | 'pending' | 'off';
</script>

<script lang="ts">
	let { status, label = '', size = 16 }: { status: Status; label?: string; size?: number } = $props();
</script>

<svg
	class={['status-icon', status]}
	width={size}
	height={size}
	viewBox="0 0 16 16"
	role={label ? 'img' : undefined}
	aria-label={label || undefined}
	aria-hidden={label ? undefined : 'true'}
	focusable="false"
>
	{#if status === 'ok'}
		<circle cx="8" cy="8" r="7" fill="currentColor" />
		<path d="M4.5 8.2l2.3 2.3 4.7-4.8" class="glyph" />
	{:else if status === 'warn'}
		<path d="M8 1.2L15.2 14.3H0.8z" fill="currentColor" />
		<path d="M8 5.8v4" class="glyph" /><circle cx="8" cy="12.1" r="0.9" class="dot" />
	{:else if status === 'error'}
		<path d="M5.1 1h5.8L15 5.1v5.8L10.9 15H5.1L1 10.9V5.1z" fill="currentColor" />
		<path d="M5.5 5.5l5 5M10.5 5.5l-5 5" class="glyph" />
	{:else if status === 'info'}
		<rect x="1" y="1" width="14" height="14" rx="3" fill="currentColor" />
		<path d="M8 7.2v4.6" class="glyph" /><circle cx="8" cy="4.6" r="0.9" class="dot" />
	{:else if status === 'pending'}
		<circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2" stroke-dasharray="3 2.2" />
	{:else}
		<circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2" />
		<path d="M4 12L12 4" stroke="currentColor" stroke-width="2" />
	{/if}
</svg>

<style>
	.status-icon {
		flex: none;
		vertical-align: -0.15em;
	}
	.glyph {
		fill: none;
		stroke: var(--color-surface);
		stroke-width: 1.8;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.dot {
		fill: var(--color-surface);
	}
	.ok {
		color: var(--color-ok);
	}
	.warn {
		color: var(--color-warn);
	}
	.error {
		color: var(--color-error);
	}
	.info {
		color: var(--color-info);
	}
	.pending,
	.off {
		color: var(--color-neutral);
	}
</style>
