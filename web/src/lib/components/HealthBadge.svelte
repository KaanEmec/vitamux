<!--
	Derived connection health (docs/architecture/connectors.md): an icon whose shape differs
	per state, always followed by its label.
-->
<script lang="ts" module>
	import type { Status } from './StatusIcon.svelte';

	export const healthLabels: Record<string, string> = {
		ok: 'Healthy',
		degraded: 'Degraded',
		failing: 'Failing',
		needs_reauth: 'Needs reauthorization',
		paused: 'Paused',
		disabled: 'Disabled',
		stale: 'Stale'
	};

	export const healthIcons: Record<string, Status> = {
		ok: 'ok',
		degraded: 'warn',
		stale: 'warn',
		failing: 'error',
		needs_reauth: 'error',
		paused: 'pending',
		disabled: 'off'
	};
</script>

<script lang="ts">
	import StatusIcon from './StatusIcon.svelte';

	let { health }: { health: string } = $props();
</script>

<span class="health">
	<StatusIcon status={healthIcons[health] ?? 'info'} />
	{healthLabels[health] ?? health}
</span>

<style>
	.health {
		display: inline-flex;
		gap: var(--space-1);
		align-items: center;
		white-space: nowrap;
	}
</style>
