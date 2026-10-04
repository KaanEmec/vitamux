<!--
	Lab results section: one h1 and links to the documents (upload and review) and to the
	confirmed results. Pages below set their own <title> and h2.
-->
<script lang="ts">
	import { page } from '$app/state';
	import Tabs from '#lib/ui/Tabs.svelte';

	let { children } = $props();

	// Review pages (/lab/documents/…) belong to the documents tab.
	const path = $derived(page.url.pathname);
	const tabs = $derived([
		{ href: '/lab', label: 'Documents', current: path === '/lab' || path.startsWith('/lab/documents/') },
		{ href: '/lab/results', label: 'Results', current: path === '/lab/results' }
	]);
</script>

<h1>Lab results</h1>
<Tabs label="Lab views" items={tabs} />

{@render children()}
