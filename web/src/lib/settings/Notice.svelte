<!-- A short confirmation or note with a status icon (shape plus colour), tinted by its status
	(base.css .inline-alert); announced politely. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import StatusIcon, { type Status } from '#lib/components/StatusIcon.svelte';

	let { status = 'ok', children }: { status?: Status; children: Snippet } = $props();

	const tone: Record<Status, string> = { ok: 'ok', warn: 'warn', error: 'error', info: 'info', pending: 'info', off: 'info' };
</script>

<p class={['inline-alert', tone[status]]} role="status"><StatusIcon {status} /> <span>{@render children()}</span></p>
