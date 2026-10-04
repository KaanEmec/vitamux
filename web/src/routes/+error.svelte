<script lang="ts">
	import { page } from '$app/state';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
</script>

<svelte:head><title>Error · Vitamux</title></svelte:head>

<main class="error-page">
	<h1><StatusIcon status="error" size={22} /> {page.status === 404 ? 'Page not found' : 'Something went wrong'}</h1>
	{#if page.error?.message && page.status !== 404}<p>{page.error.message}</p>{/if}
	<p><a href="/">Back to the dashboard</a></p>
</main>

<style>
	.error-page {
		max-width: 40rem;
		margin: var(--space-8) auto;
		padding: 0 var(--space-4);
	}
</style>
