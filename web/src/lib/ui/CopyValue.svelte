<!--
	A value to copy exactly (a callback URL, a line for .env, a secret shown once): its label, the
	value in mono and a Copy button. Where the clipboard is blocked the value is selected instead.
-->
<script lang="ts">
	let { label, value }: { label: string; value: string } = $props();

	let copied = $state(false);
	let code = $state<HTMLElement>();

	async function copy() {
		try {
			await navigator.clipboard.writeText(value);
			copied = true;
			setTimeout(() => (copied = false), 2000);
		} catch {
			if (code) getSelection()?.selectAllChildren(code);
		}
	}
</script>

<div class="copy">
	<span class="label">{label}</span>
	<div class="row">
		<code bind:this={code}>{value}</code>
		<button class="btn sm" type="button" onclick={copy} aria-label="{copied ? 'Copied' : 'Copy'} {label}">{copied ? 'Copied' : 'Copy'}</button>
	</div>
	<span class="visually-hidden" role="status">{copied ? `${label} copied` : ''}</span>
</div>

<style>
	.copy {
		display: grid;
		gap: var(--space-1);
		margin-bottom: var(--space-3);
	}
	.label {
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.row {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	code {
		flex: 1;
		min-width: 0;
		padding: var(--space-2) var(--space-3);
		font-family: var(--font-mono);
		font-size: var(--text-sm);
		overflow-wrap: anywhere;
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
</style>
