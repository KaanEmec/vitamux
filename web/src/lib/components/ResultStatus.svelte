<!--
	Data status of a resolved value (direct, fallback, calculated, overridden, partial, no data):
	its shape and colour (lib/ui/status.ts), always followed by its word.
-->
<script lang="ts">
	import { dataStatus, displayStatus, ringPath } from '../ui/status.ts';

	let { status, partial = false }: { status: string; partial?: boolean } = $props();

	const s = $derived(displayStatus(status, partial));
	const g = $derived(dataStatus[s]);
</script>

<span class={['result-status', s]}>
	<svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true" focusable="false">
		{#if g.ring}<path d={ringPath} class="ring" />{/if}
		{#if g.shape}<path d={g.shape} class="shape" />{/if}
	</svg>
	{g.label}
</span>

<style>
	.result-status {
		display: inline-flex;
		gap: var(--space-2);
		align-items: center;
		white-space: nowrap;
	}
	svg {
		flex: none;
	}
	.shape {
		fill: currentColor;
	}
	.ring {
		fill: none;
		stroke: currentColor;
		stroke-width: 1.4;
	}
	.direct svg {
		color: var(--status-direct);
	}
	.fallback svg {
		color: var(--status-fallback);
	}
	.calculated svg {
		color: var(--status-calculated);
	}
	.overridden svg {
		color: var(--status-overridden);
	}
	.partial svg {
		color: var(--status-partial);
	}
	.no_data svg {
		color: var(--status-none);
	}
</style>
