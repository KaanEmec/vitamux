<!-- A source chip (base.css .chip): the source's colour dot and a label. `dashed` marks a source that was not selected. -->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { sourceClass } from './source.ts';

	let { source, dashed = false, children }: { source?: string; dashed?: boolean; children: Snippet } = $props();
</script>

<span class={['chip', source && sourceClass(source), dashed && 'dashed']}>
	{#if source}<span class="dot" aria-hidden="true"></span>{/if}
	{@render children()}
</span>
