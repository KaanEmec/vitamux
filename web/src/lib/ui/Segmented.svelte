<!--
	A row of mutually exclusive toggle buttons (aria-pressed), e.g. the theme or a chart range
	(base.css .segmented: the selected option is a light pill on the dark track).
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
