<!--
	Modal dialog on the native <dialog> element: focus is trapped and Escape closes it.
	Mount it only while it is needed; `onclose` fires for Escape, the close button and a
	backdrop click. Focus returns to the element that opened it.
-->
<script lang="ts">
	import { onMount, type Snippet } from 'svelte';

	let { title, onclose, children }: { title: string; onclose: () => void; children: Snippet } = $props();

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
<dialog bind:this={dialog} aria-labelledby={titleId} {onclose} onclick={backdropClick}>
	<div class="head">
		<h2 id={titleId}>{title}</h2>
		<button class="btn" type="button" onclick={() => dialog.close()}>Close</button>
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
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
		box-shadow: var(--shadow-1);
	}
	dialog::backdrop {
		background: rgb(0 0 0 / 0.45);
	}
	.head {
		display: flex;
		gap: var(--space-3);
		align-items: flex-start;
		justify-content: space-between;
	}
</style>
