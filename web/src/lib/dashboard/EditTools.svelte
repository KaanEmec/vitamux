<!--
	The edit-mode controls of one card: a drag handle, move earlier and later (the keyboard
	alternative to dragging), S/M/L size and hide.
-->
<script lang="ts">
	import Button from '../ui/Button.svelte';
	import Icon from '../ui/Icon.svelte';
	import Segmented from '../ui/Segmented.svelte';
	import { dashIcons } from './icons.ts';
	import { sizes, type Size } from './layout.ts';

	let {
		label,
		size,
		first,
		last,
		onsize,
		onmove,
		onhide,
		ondrag,
		ondragend
	}: {
		label: string;
		size: Size;
		first: boolean;
		last: boolean;
		onsize: (s: Size) => void;
		onmove: (dir: -1 | 1) => void;
		onhide: () => void;
		ondrag: (e: DragEvent) => void;
		ondragend: () => void;
	} = $props();
</script>

<div class="tools">
	<span class="grip" role="img" aria-label="Drag to reorder {label}" title="Drag to reorder" draggable="true" ondragstart={ondrag} {ondragend}>
		<Icon d={dashIcons.grip} size={18} />
	</span>
	<Button variant="ghost" size="sm" icon={dashIcons.up} aria-label="Move {label} earlier" disabled={first} onclick={() => onmove(-1)} />
	<Button variant="ghost" size="sm" icon={dashIcons.down} aria-label="Move {label} later" disabled={last} onclick={() => onmove(1)} />
	<span class="size">
		<Segmented label="Size of {label}" options={sizes.map((s) => ({ value: s, label: s }))} value={size} onchange={onsize} />
	</span>
	<Button variant="ghost" size="sm" icon={dashIcons.hide} aria-label="Hide {label}" title="Hide" onclick={onhide} />
</div>

<style>
	.tools {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 2px;
		margin-bottom: var(--space-1);
	}
	.grip {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 1.75rem;
		height: var(--control-h-sm);
		color: var(--color-text-muted);
		cursor: grab;
	}
	.size {
		margin-left: auto;
		font-size: var(--text-xs);
	}
</style>
