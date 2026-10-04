<!--
	"Why this value?" disclosure: a button that shows the result's explanation (and its
	warnings) in a small panel. Escape or a click elsewhere closes it.
-->
<script lang="ts">
	let { text, warnings = [], label = 'Explain' }: { text: string; warnings?: string[]; label?: string } = $props();

	let open = $state(false);
	let root: HTMLElement;
	const panelId = $props.id();

	function outside(e: MouseEvent) {
		if (open && !root.contains(e.target as Node)) open = false;
	}
	function keydown(e: KeyboardEvent) {
		if (open && e.key === 'Escape') {
			open = false;
			root.querySelector('button')?.focus();
		}
	}
</script>

<svelte:window onclick={outside} onkeydown={keydown} />

<span class="explain" bind:this={root}>
	<button class="btn link" type="button" aria-expanded={open} aria-controls={panelId} onclick={() => (open = !open)}>
		{label}
	</button>
	<div id={panelId} class="panel" hidden={!open} role="region" aria-label="Explanation">
		<p>{text}</p>
		{#if warnings.length}
			<p class="warnings">Warnings: {warnings.join(', ')}</p>
		{/if}
	</div>
</span>

<style>
	.explain {
		position: relative;
		display: inline-block;
	}
	.panel {
		position: absolute;
		z-index: 5;
		top: 100%;
		right: 0;
		width: 22rem;
		max-width: 80vw;
		padding: var(--space-3) var(--space-4);
		margin-top: var(--space-1);
		background: var(--color-surface);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-md);
		box-shadow: var(--shadow-2);
		font-size: var(--text-sm);
		white-space: normal;
	}
	.panel[hidden] {
		display: none;
	}
	p {
		margin: 0;
	}
	.warnings {
		margin-top: var(--space-2);
		color: var(--color-warn);
	}
</style>
