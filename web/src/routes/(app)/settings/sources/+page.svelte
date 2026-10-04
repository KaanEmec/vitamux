<!-- Sources: the owner's provider applications and the sidecars (docs/adr/0021-source-setup.md). -->
<script lang="ts">
	import AppCredentials from '#lib/setup/AppCredentials.svelte';
	import Sidecars from '#lib/setup/Sidecars.svelte';
	import SourceOrder from '#lib/settings/SourceOrder.svelte';
</script>

<svelte:head><title>Sources · Vitamux</title></svelte:head>

<p class="lede">
	What Vitamux needs to reach your sources. Secrets are write-only: Vitamux stores them encrypted and never shows them again. To connect an account, use
	<a href="/connections">Connections</a>.
</p>

<SourceOrder />
<AppCredentials />
<Sidecars />
