<!-- Whether a connection uses an official API, an unofficial one, or receives pushed uploads. -->
<script lang="ts">
	import UnofficialBadge from '../components/UnofficialBadge.svelte';
	import Badge from '../ui/Badge.svelte';

	let { official }: { official: boolean | null } = $props();
</script>

{#if official === false}
	<UnofficialBadge />
{:else if official}
	<Badge tone="accent">Official</Badge>
{:else}
	<Badge>Push</Badge>
{/if}
