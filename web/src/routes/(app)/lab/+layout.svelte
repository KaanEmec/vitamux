<!--
	Lab results section: one h1 and links to the documents (upload and review) and to the
	confirmed results. A review (/lab/documents/…) is a focused view with its own breadcrumb and
	h1. Pages set their own <title>.
-->
<script lang="ts">
	import { page } from '$app/state';
	import Tabs from '#lib/ui/Tabs.svelte';

	let { children } = $props();

	const path = $derived(page.url.pathname);
	const review = $derived(path.startsWith('/lab/documents/'));
	const tabs = $derived([
		{ href: '/lab', label: 'Documents', current: path === '/lab' },
		{ href: '/lab/results', label: 'Results', current: path === '/lab/results' || path.startsWith('/lab/analytes/') }
	]);
</script>

{#if !review}
	<h1>Lab results</h1>
	<Tabs label="Lab views" items={tabs} />
{/if}

{@render children()}
