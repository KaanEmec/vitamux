<!-- A thin progress bar (native <progress>, so it is announced); `label` names what is progressing. -->
<script lang="ts">
	let { value, max, label }: { value: number; max: number; label: string } = $props();
</script>

<progress {value} max={max || 1} aria-label={label}></progress>

<style>
	progress {
		display: block;
		width: 100%;
		height: 0.5rem;
		overflow: hidden;
		appearance: none;
		background: var(--color-surface-2);
		border: 0;
		border-radius: var(--radius-pill);
	}
	progress::-webkit-progress-bar {
		background: var(--color-surface-2);
	}
	progress::-webkit-progress-value {
		background: var(--color-accent);
		border-radius: var(--radius-pill);
	}
	progress::-moz-progress-bar {
		background: var(--color-accent);
		border-radius: var(--radius-pill);
	}
</style>
