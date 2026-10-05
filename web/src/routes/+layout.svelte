<script lang="ts">
	import '@fontsource-variable/geist';
	import '@fontsource-variable/geist-mono';
	import '#lib/styles/tokens.css';
	import '#lib/styles/base.css';
	import '#lib/prefs.svelte.ts'; // applies the stored theme before the first render
	import { beforeNavigate } from '$app/navigation';
	import { updated } from '$app/state';

	// After an upgrade the old build's chunks are gone: load the next page from the server instead.
	beforeNavigate(({ willUnload, to }) => {
		if (updated.current && !willUnload && to?.url) location.href = to.url.href;
	});

	let { children } = $props();
</script>

{@render children()}
