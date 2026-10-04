<!--
	"Add metric": the catalogue (GET /metrics) by section, searchable. Sleep stages and blood
	pressure parts are offered as the one card they belong to. `pinned` holds the cards on the layout.
-->
<script lang="ts">
	import Modal from '../components/Modal.svelte';
	import { cardLabel, cardOf, type Catalogue } from './layout.ts';

	let {
		catalogue,
		pinned,
		onpin,
		onunpin,
		onclose
	}: { catalogue: Catalogue[]; pinned: string[]; onpin: (code: string) => void; onunpin: (code: string) => void; onclose: () => void } = $props();

	let query = $state('');

	// One entry per card, grouped by catalogue section in catalogue order.
	const sections = $derived.by(() => {
		const q = query.trim().toLowerCase();
		const out: [string, { code: string; label: string; meta: string }[]][] = [];
		const seen: string[] = [];
		for (const m of catalogue) {
			const code = cardOf(m);
			if (seen.includes(code)) continue;
			seen.push(code);
			const label = cardLabel(code);
			if (q && !label.toLowerCase().includes(q) && !code.includes(q)) continue;
			const section = code === 'sleep' ? 'Sleep' : code === 'blood_pressure' ? 'Blood pressure' : m.section;
			const item = { code, label, meta: code === m.code ? m.unit : '' };
			const group = out.find(([name]) => name === section);
			if (group) group[1].push(item);
			else out.push([section, [item]]);
		}
		return out;
	});
</script>

<Modal title="Add metric" drawer="right" {onclose}>
	<div class="field">
		<label for="add-search">Search the catalogue</label>
		<input id="add-search" type="search" placeholder="e.g. weight, body fat, steps" bind:value={query} autocomplete="off" />
	</div>
	{#each sections as [section, items] (section)}
		<section>
			<h3>{section}</h3>
			<ul>
				{#each items as m (m.code)}
					<li>
						<span class="name">{m.label}{#if m.meta}<span class="muted"> · {m.meta}</span>{/if}</span>
						{#if pinned.includes(m.code)}
							<button class="btn sm" type="button" aria-label="Unpin {m.label}" aria-pressed="true" onclick={() => onunpin(m.code)}>Pinned</button>
						{:else}
							<button class="btn sm" type="button" aria-label="Pin {m.label}" onclick={() => onpin(m.code)}>Pin</button>
						{/if}
					</li>
				{/each}
			</ul>
		</section>
	{:else}
		<p class="muted">Nothing in the catalogue matches “{query}”.</p>
	{/each}
</Modal>

<style>
	h3 {
		margin: var(--space-4) 0 var(--space-1);
		font-size: var(--text-xs);
		font-weight: 600;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--color-text-muted);
	}
	ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		min-height: 3rem;
		border-bottom: 1px solid var(--color-border);
	}
	.name {
		flex: 1;
		font-size: var(--text-md);
	}
</style>
