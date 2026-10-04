<!--
	Labelled input with an optional hint and error (from fieldErrors). `prefix` and `suffix` put
	short text inside the border (a unit, a currency, a domain); `multiline` renders a <textarea>
	(`rows`). Extra attributes (autocomplete, inputmode, required, …) pass through to the control.
-->
<script lang="ts">
	import type { HTMLInputAttributes, HTMLTextareaAttributes } from 'svelte/elements';

	let {
		label,
		name,
		value = $bindable(''),
		error = '',
		hint = '',
		prefix = '',
		suffix = '',
		multiline = false,
		rows = 4,
		input = $bindable(),
		...rest
	}: {
		label: string;
		name: string;
		value?: string;
		error?: string;
		hint?: string;
		prefix?: string;
		suffix?: string;
		multiline?: boolean;
		rows?: number;
		input?: HTMLInputElement;
	} & Omit<HTMLInputAttributes, 'value' | 'name' | 'prefix'> = $props();

	const id = $props.id();
	const describedBy = $derived([hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(' ') || undefined);
</script>

{#snippet control()}
	<input
		{...rest}
		{id}
		{name}
		bind:value
		bind:this={input}
		aria-invalid={error ? 'true' : undefined}
		aria-describedby={describedBy}
	/>
{/snippet}

<div class="field">
	<label for={id}>{label}</label>
	{#if multiline}
		<textarea
			{...rest as HTMLTextareaAttributes}
			{id}
			{name}
			{rows}
			bind:value
			aria-invalid={error ? 'true' : undefined}
			aria-describedby={describedBy}
		></textarea>
	{:else if prefix || suffix}
		<div class="input-group">
			{#if prefix}<span class="affix">{prefix}</span>{/if}
			{@render control()}
			{#if suffix}<span class="affix">{suffix}</span>{/if}
		</div>
	{:else}
		{@render control()}
	{/if}
	{#if hint}<span class="hint" id="{id}-hint">{hint}</span>{/if}
	{#if error}<span class="error" id="{id}-error">{error}</span>{/if}
</div>
