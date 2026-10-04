<!--
	An on/off setting that applies at once: a checkbox with role="switch" (base.css) beside its
	label and an optional hint. Use a checkbox instead when the choice is saved with a form.
	Extra attributes (name, disabled, onchange, …) pass through to the <input>.
-->
<script lang="ts">
	import type { HTMLInputAttributes } from 'svelte/elements';

	let {
		label,
		hint = '',
		checked = $bindable(false),
		...rest
	}: { label: string; hint?: string; checked?: boolean } & Omit<HTMLInputAttributes, 'type' | 'role' | 'checked'> = $props();

	const id = $props.id();
</script>

<label class="check">
	<input {...rest} type="checkbox" role="switch" bind:checked aria-describedby={hint ? `${id}-hint` : undefined} />
	<span>{label}{#if hint}<span class="hint" id="{id}-hint">{hint}</span>{/if}</span>
</label>
