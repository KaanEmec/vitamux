<!-- A short confirmation or note with a status icon (shape plus colour); announced politely. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import StatusIcon, { type Status } from '#lib/components/StatusIcon.svelte';

	let { status = 'ok', children }: { status?: Status; children: Snippet } = $props();
</script>

<p class="notice" role="status"><StatusIcon {status} /> <span>{@render children()}</span></p>

<style>
	.notice {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		margin: 0 0 var(--space-4);
	}
	.notice :global(.status-icon) {
		margin-top: 0.3em;
	}
</style>
