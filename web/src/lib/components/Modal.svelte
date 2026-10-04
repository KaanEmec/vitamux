<!--
	Modal dialog on the native <dialog> element: focus is trapped and Escape closes it.
	Mount it only while it is needed; `onclose` fires for Escape, the close button and a
	backdrop click. Focus returns to the element that opened it. `drawer` makes it a side
	sheet (a bottom sheet for "right" on narrow screens).
-->
<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import Icon from '../ui/Icon.svelte';
	import { icons } from '../ui/icons.ts';

	let {
		title,
		onclose,
		drawer,
		children
	}: { title: string; onclose: () => void; drawer?: 'left' | 'right'; children: Snippet } = $props();

	let dialog: HTMLDialogElement;
	const titleId = $props.id();

	onMount(() => {
		const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
		dialog.showModal();
		return () => opener?.focus();
	});

	function backdropClick(e: MouseEvent) {
		if (e.target === dialog) dialog.close();
	}
</script>

<!-- The backdrop click is a mouse convenience; Escape and the close button are the keyboard paths. -->
<dialog bind:this={dialog} class={drawer && `drawer ${drawer}`} aria-labelledby={titleId} {onclose} onclick={backdropClick}>
	<div class="head">
		<h2 id={titleId}>{title}</h2>
		<button class="btn ghost sm close" type="button" aria-label="Close" onclick={() => dialog.close()}>
			<Icon d={icons.close} size={18} />
		</button>
	</div>
	{@render children()}
</dialog>

<style>
	dialog {
		width: min(44rem, calc(100vw - 2rem));
		max-height: calc(100vh - 2rem);
		padding: var(--space-5);
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-2);
	}
	dialog::backdrop {
		background: rgb(0 0 0 / 0.5);
	}
	.head {
		display: flex;
		gap: var(--space-3);
		align-items: flex-start;
		justify-content: space-between;
		margin-bottom: var(--space-2);
	}
	.close {
		width: var(--control-h-sm);
		padding: 0;
		color: var(--color-text-muted);
	}
	.drawer {
		width: min(26rem, 100vw);
		max-width: none;
		height: 100dvh;
		max-height: none;
		margin: 0;
		border-radius: 0;
	}
	.drawer.left {
		margin-right: auto;
		border-width: 0 1px 0 0;
	}
	.drawer.right {
		margin-left: auto;
		border-width: 0 0 0 1px;
	}
	@media (max-width: 48rem) {
		.drawer.right {
			width: 100vw;
			height: auto;
			max-height: 85dvh;
			margin: auto 0 0;
			border-width: 1px 0 0;
			border-radius: var(--radius-lg) var(--radius-lg) 0 0;
		}
	}
</style>
