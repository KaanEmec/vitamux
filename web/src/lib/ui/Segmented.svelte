<!--
	A row of mutually exclusive toggle buttons (aria-pressed), e.g. the theme or a chart range.
	Options with an `icon` show only the icon and use `label` as the accessible name.
-->
<script lang="ts" generics="T extends string">
	import Icon from './Icon.svelte';

	let {
		label,
		options,
		value = $bindable(),
		onchange
	}: {
		label: string;
		options: { value: T; label: string; icon?: string }[];
		value: T;
		onchange?: (value: T) => void;
	} = $props();
</script>

<div class="segmented" role="group" aria-label={label}>
	{#each options as o (o.value)}
		<button
			type="button"
			aria-pressed={o.value === value}
			aria-label={o.icon ? o.label : undefined}
			title={o.icon ? o.label : undefined}
			onclick={() => {
				value = o.value;
				onchange?.(o.value);
			}}
		>
			{#if o.icon}<Icon d={o.icon} size={16} />{:else}{o.label}{/if}
		</button>
	{/each}
</div>

<style>
	.segmented {
		display: inline-flex;
		gap: 2px;
		padding: 3px;
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	button {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		min-width: 2.25rem;
		min-height: 2.125rem;
		padding: 0 var(--space-3);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
		background: transparent;
		border: 0;
		border-radius: var(--radius-xs);
		cursor: pointer;
	}
	button:has(:global(.icon)) {
		padding: 0;
	}
	button:hover {
		color: var(--color-text);
	}
	button[aria-pressed='true'] {
		font-weight: 600;
		color: var(--color-text);
		background: var(--color-selected);
	}
</style>
