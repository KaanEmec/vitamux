<!--
	Modal dialog on the native <dialog> element: focus is trapped and Escape closes it.
	Mount it only while it is needed; `onclose` fires for Escape, the close button and a
	backdrop click. Focus returns to the element that opened it. `drawer` makes it a side
	sheet (a bottom sheet for "right" on narrow screens). The header holds the title and the
	close button; the body scrolls; `footer` (optional) keeps its actions in view below it.
-->
<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import Icon from '../ui/Icon.svelte';
	import { icons } from '../ui/icons.ts';

	let {
		title,
		onclose,
		drawer,
		footer,
		children
	}: { title: string; onclose: () => void; drawer?: 'left' | 'right'; footer?: Snippet; children: Snippet } = $props();

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
		<button class="btn ghost sm icon-btn" type="button" aria-label="Close" onclick={() => dialog.close()}>
			<Icon d={icons.close} size={18} />
		</button>
	</div>
	<div class="body">{@render children()}</div>
	{#if footer}<div class="foot">{@render footer()}</div>{/if}
</dialog>

<style>
	dialog {
		width: min(44rem, calc(100vw - 2rem));
		max-height: calc(100dvh - 2rem);
		padding: 0;
		color: var(--color-text);
		background: var(--card-bg);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-lg);
		box-shadow: var(--shadow-2);
	}
	dialog[open] {
		display: flex;
		flex-direction: column;
	}
	dialog::backdrop {
		background: var(--color-backdrop);
		backdrop-filter: blur(6px);
	}
	.head {
		display: flex;
		flex: none;
		gap: var(--space-3);
		align-items: center;
		justify-content: space-between;
		padding: var(--space-3) var(--space-3) var(--space-3) var(--space-5);
		border-bottom: 1px solid var(--color-border);
	}
	h2 {
		margin: 0;
	}
	.head .btn {
		flex: none;
	}
	.body {
		flex: 1;
		min-height: 0;
		padding: var(--space-5);
		overflow: auto;
	}
	.foot {
		display: flex;
		flex: none;
		flex-wrap: wrap;
		gap: var(--space-2);
		justify-content: flex-end;
		padding: var(--space-3) var(--space-5);
		background: var(--color-surface);
		border-top: 1px solid var(--color-border);
	}
	@media (prefers-reduced-motion: no-preference) {
		dialog[open] {
			animation: dialog-in 180ms var(--ease);
		}
		dialog[open]::backdrop {
			animation: backdrop-in 180ms var(--ease);
		}
		.drawer.left[open] {
			animation-name: drawer-left;
		}
		.drawer.right[open] {
			animation-name: drawer-right;
		}
	}
	@keyframes dialog-in {
		from {
			translate: 0 0.5rem;
			scale: 0.98;
		}
	}
	@keyframes backdrop-in {
		from {
			opacity: 0;
		}
	}
	@keyframes drawer-left {
		from {
			translate: -100% 0;
		}
	}
	@keyframes drawer-right {
		from {
			translate: 100% 0;
		}
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
		.drawer.right[open] {
			animation-name: drawer-up;
		}
	}
	@keyframes drawer-up {
		from {
			translate: 0 100%;
		}
	}
</style>
