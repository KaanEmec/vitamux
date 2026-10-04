<!--
	Data section: one h1 and links to the daily view, sleep comparison and workouts. The
	pages below set their own <title> and an h2.
-->
<script lang="ts">
	import { page } from '$app/state';
	import Tabs from '#lib/ui/Tabs.svelte';

	let { children } = $props();

	// Drilldowns (/data/day/…) belong to the daily view.
	const path = $derived(page.url.pathname);
	const tabs = $derived([
		{ href: '/data', label: 'Daily values', current: path === '/data' || path.startsWith('/data/day/') },
		{ href: '/data/sleep', label: 'Sleep', current: path === '/data/sleep' },
		{ href: '/data/workouts', label: 'Workouts', current: path === '/data/workouts' }
	]);
</script>

<h1>Data</h1>
<Tabs label="Data views" items={tabs} />

{@render children()}
