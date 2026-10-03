<!--
	Labelled input with an optional hint and error (from fieldErrors). Extra attributes
	(autocomplete, inputmode, required, …) pass through to the <input>.
-->
<script lang="ts">
	import type { HTMLInputAttributes } from 'svelte/elements';

	let {
		label,
		name,
		value = $bindable(''),
		error = '',
		hint = '',
		input = $bindable(),
		...rest
	}: {
		label: string;
		name: string;
		value?: string;
		error?: string;
		hint?: string;
		input?: HTMLInputElement;
	} & Omit<HTMLInputAttributes, 'value' | 'name'> = $props();

	const id = $props.id();
	const describedBy = $derived([hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(' ') || undefined);
</script>

<div class="field">
	<label for={id}>{label}</label>
	<input
		{...rest}
		{id}
		{name}
		bind:value
		bind:this={input}
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={describedBy}
	/>
	{#if hint}<span class="hint" id="{id}-hint">{hint}</span>{/if}
	{#if error}<span class="error" id="{id}-error">{error}</span>{/if}
</div>
