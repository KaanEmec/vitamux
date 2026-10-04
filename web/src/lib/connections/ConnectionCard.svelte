<!-- A source on the Connections list: health, last sync, the 14-day run strip and its action. -->
<script lang="ts">
	import HealthBadge from '../components/HealthBadge.svelte';
	import ConnectionActions from './ConnectionActions.svelte';
	import KindBadge from './KindBadge.svelte';
	import Monogram from './Monogram.svelte';
	import RunStrip from './RunStrip.svelte';
	import { alerting, lastSync, modes, providerLabel, type Connection } from './connections.ts';
	import type { Run } from './runs.ts';

	let { connection, runs, onchange }: { connection: Connection; runs: Run[] | null | undefined; onchange: (c: Connection) => void } = $props();

	const nameId = $props.id();
</script>

<article class={['card', alerting.includes(connection.health) && 'attention']} aria-labelledby={nameId}>
	<header>
		<Monogram provider={connection.provider} />
		<div class="who">
			<h2 id={nameId}><a href="/connections/{connection.id}">{providerLabel(connection.provider)}</a></h2>
			<div class="muted mode">{modes[connection.mode] ?? connection.mode}</div>
		</div>
		<KindBadge official={connection.official} />
	</header>
	<div>
		<p class="state"><HealthBadge health={connection.health} /> <span class="muted">{lastSync(connection)}</span></p>
		{#if connection.health_reason}<p class="muted reason">{connection.health_reason}</p>{/if}
	</div>
	{#if connection.mode !== 'push'}<RunStrip {runs} />{/if}
	<div class="foot"><ConnectionActions {connection} compact {onchange} /></div>
</article>

<style>
	article {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
	}
	.attention {
		border-color: color-mix(in srgb, var(--color-warn) 55%, transparent);
	}
	header {
		display: flex;
		gap: var(--space-3);
		align-items: center;
	}
	.who {
		flex: 1;
		min-width: 0;
	}
	h2 {
		margin: 0;
		font-size: var(--text-lg);
	}
	h2 a {
		color: inherit;
		text-decoration: none;
	}
	h2 a:hover {
		text-decoration: underline;
	}
	.mode,
	.reason {
		font-size: var(--text-xs);
	}
	.state,
	.reason {
		margin: 0;
	}
	.state {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		font-size: var(--text-sm);
	}
	.state :global(.health) {
		font-weight: 600;
	}
	.foot {
		margin-top: auto;
	}
</style>
