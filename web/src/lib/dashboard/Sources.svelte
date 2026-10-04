<!-- One health card per connection (the old Today page's "Connections" section), or the way to connect a first source. -->
<script lang="ts">
	import HealthBadge from '../components/HealthBadge.svelte';
	import UnofficialBadge from '../components/UnofficialBadge.svelte';
	import { ago, providerLabel, type Connection } from '../connections/connections.ts';
	import { sourceClass } from '../ui/source.ts';

	let { connections }: { connections: Connection[] } = $props();
</script>

<section aria-labelledby="connections">
	<h2 id="connections">Connections</h2>
	{#if connections.length}
		<ul>
			{#each connections as c (c.id)}
				<li class={['card', sourceClass(c.provider)]}>
					<h3>
						<span class="dot" aria-hidden="true"></span>
						<a href="/connections/{c.id}">{providerLabel(c.provider)}</a>
						{#if c.official === false}<UnofficialBadge />{/if}
					</h3>
					<p><HealthBadge health={c.health} /></p>
					<p class="muted">Last success {ago(c.last_success_at)}</p>
					{#if c.health_reason}<p class="muted">{c.health_reason}</p>{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="muted">No connections yet. <a href="/connections">Connect a source</a>.</p>
	{/if}
</section>

<style>
	h2 {
		font-size: var(--text-lg);
	}
	ul {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr));
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: grid;
		align-content: start;
		gap: var(--space-2);
		padding: var(--space-4);
		font-size: var(--text-sm);
	}
	h3 {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin: 0;
		font-size: var(--text-md);
	}
	.dot {
		width: 0.625rem;
		height: 0.625rem;
		background: var(--src, var(--color-neutral));
		border-radius: 3px;
	}
	p {
		margin: 0;
	}
</style>
