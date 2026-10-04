<!--
	Explore (E21, J21.8): every metric, group, event type and lab analyte with data. Until it
	lands, this page points to the existing data views.
-->
<script lang="ts">
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
</script>

<svelte:head><title>Explore · Vitamux</title></svelte:head>

<h1>Explore</h1>
<EmptyState
	icon={icons.explore}
	title="Explore is on its way"
	text="It will list everything Vitamux has stored. Until then, daily values, sleep and workouts are in the data views."
>
	<Button href="/data">Daily values</Button>
	<Button href="/data/sleep">Sleep</Button>
	<Button href="/data/workouts">Workouts</Button>
</EmptyState>
