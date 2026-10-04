<script lang="ts">
	import '@fontsource-variable/geist';
	import '@fontsource-variable/geist-mono';
	import '#lib/styles/tokens.css';
	import '#lib/styles/base.css';
	import '#lib/prefs.svelte.ts'; // applies the stored theme before the first render

	let { children } = $props();
</script>

{@render children()}
