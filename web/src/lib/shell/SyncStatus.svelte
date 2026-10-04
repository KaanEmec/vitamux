<!--
	Sync-status pill in the top bar: how long ago the latest successful sync finished, or how
	many connections need attention. Links to Connections. Hidden until connections load.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import { alerting } from '../connections/connections.ts';

	let { connections }: { connections: Schemas['Connection'][] } = $props();

	const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
	const attention = $derived(connections.filter((c) => alerting.includes(c.health)).length);
	const last = $derived(Math.max(0, ...connections.map((c) => (c.last_success_at ? Date.parse(c.last_success_at) : 0))));

	function ago(t: number): string {
		const min = Math.round((t - Date.now()) / 60_000);
		if (min > -60) return rtf.format(min, 'minute');
		if (min > -48 * 60) return rtf.format(Math.round(min / 60), 'hour');
		return rtf.format(Math.round(min / 1440), 'day');
	}
</script>

{#if connections.length}
	<a class={['pill', attention && 'warn']} href="/connections">
		<span class="dot" aria-hidden="true"></span>
		{#if attention}
			{attention} {attention === 1 ? 'source needs' : 'sources need'} attention
		{:else if last}
			Synced {ago(last)}
		{:else}
			Not synced yet
		{/if}
	</a>
{/if}

<style>
	.pill {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		min-height: var(--control-h);
		padding: 0 var(--space-4);
		font-size: var(--text-sm);
		color: var(--color-text-muted);
		white-space: nowrap;
		text-decoration: none;
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-pill);
	}
	.pill:hover {
		color: var(--color-text);
	}
	.dot {
		width: 0.5rem;
		height: 0.5rem;
		background: var(--color-ok);
		border-radius: 50%;
	}
	/* Attention is a triangle, not only a colour. */
	.warn .dot {
		width: 0.625rem;
		background: var(--color-warn);
		border-radius: 0;
		clip-path: polygon(50% 0, 100% 100%, 0 100%);
	}
</style>
