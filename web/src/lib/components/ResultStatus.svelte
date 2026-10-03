<!--
	Status of a resolved value (direct, fallback, calculated, overridden, no_data): an icon
	whose shape differs per status, always followed by its text label.
-->
<script lang="ts" module>
	export const statusLabels: Record<string, string> = {
		direct: 'Direct',
		fallback: 'Fallback',
		calculated: 'Calculated',
		overridden: 'Overridden',
		no_data: 'No data'
	};
</script>

<script lang="ts">
	import StatusIcon, { type Status } from './StatusIcon.svelte';

	let { status }: { status: string } = $props();

	const icon: Record<string, Status> = { direct: 'ok', fallback: 'warn', calculated: 'info', no_data: 'off' };
</script>

<span class="result-status">
	{#if status === 'overridden'}
		<!-- Diamond with a pen stroke: manual value, distinct from every StatusIcon shape. -->
		<svg class="override-icon" width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
			<path d="M8 .8L15.2 8 8 15.2.8 8z" fill="currentColor" />
			<path d="M5.2 10.8l.5-2 4.2-4.2 1.5 1.5-4.2 4.2z" class="glyph" />
		</svg>
	{:else}
		<StatusIcon status={icon[status] ?? 'pending'} />
	{/if}
	{statusLabels[status] ?? status}
</span>

<style>
	.result-status {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		white-space: nowrap;
	}
	.override-icon {
		flex: none;
		color: var(--color-accent);
	}
	.glyph {
		fill: none;
		stroke: var(--color-surface);
		stroke-width: 1.4;
		stroke-linejoin: round;
	}
</style>
