<!--
	Rule lens (J21.10) placeholder, replaced by the Rules stream. Contract with the metric detail
	page: props { metric, start, end, ondraft }, start and end inclusive local dates; ondraft
	passes the draft's windows ({ local_date, value, changed }) or null to clear the overlay.
-->
<script lang="ts">
	type Draft = { local_date: string; value: number | null; changed: boolean }[];

	let { metric, start, end, ondraft }: { metric: string; start: string; end: string; ondraft: (draft: Draft | null) => void } = $props();

	$effect(() => () => ondraft(null));
</script>

<aside class="card lens" aria-label="Rule lens">
	<h2>How it’s calculated</h2>
	<p class="muted">Previewing rule changes for {start} to {end} on this chart is coming soon.</p>
	<p><a href="/rules/{encodeURIComponent(metric)}">Rule versions</a> · <a href="/rules/new?metric={encodeURIComponent(metric)}">Open the rule builder</a></p>
</aside>

<style>
	.lens p {
		margin: 0 0 var(--space-2);
	}
</style>
