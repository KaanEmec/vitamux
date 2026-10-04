<!--
	One window of the metric detail chart: its resolved value, status and explanation, the rule's
	inputs with their records (provenance, exclude), and the override actions. The all-sources
	day view has the full drilldown and the window's overrides.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import OverrideDialog, { type OverrideAction } from '../components/OverrideDialog.svelte';
	import ProvenanceDialog from '../components/ProvenanceDialog.svelte';
	import ResultStatus from '../components/ResultStatus.svelte';
	import { formatValue } from '../data/format.ts';
	import { formatInstant } from '../charts/scale.ts';
	import Icon from '../ui/Icon.svelte';
	import { icons } from '../ui/icons.ts';
	import { dayMs } from './series.ts';

	let {
		metric,
		date,
		value,
		onchanged,
		onclose
	}: { metric: string; date: string; value?: Schemas['ResolvedValue']; onchanged: () => void; onclose: () => void } = $props();

	let override = $state<{ action: OverrideAction; inputId: string } | null>(null);
	let provenance = $state<string | null>(null);

	const headingId = $props.id();
	const kind = $derived((value?.window?.kind ?? 'local_day') as Schemas['OverrideWindow']['kind']);
	const groups = $derived((value?.inputs ?? []).flatMap((i) => (i.group ? [i.group] : [])));
	const warnings = $derived((value?.warnings ?? []).map((w) => (w.group ? `${w.code} (${w.group})` : w.code)));
</script>

<section class="card point" aria-labelledby={headingId}>
	<div class="head">
		<h2 id={headingId}>{formatInstant(dayMs(date), 'UTC', false)}</h2>
		<button class="btn ghost sm" type="button" aria-label="Close the selected window" onclick={onclose}><Icon d={icons.close} size={16} /></button>
	</div>
	{#if value}
		<p class="headline">
			<ResultStatus status={value.status} partial={value.partial} />
			<strong class="value">{value.status === 'no_data' ? '–' : formatValue(value.value, value.unit)}</strong>
			{#if value.selected}<span class="muted">from {value.selected}</span>{/if}
		</p>
		<p>{value.explanation}</p>
		{#if warnings.length}<p class="warn">Warnings: {warnings.join(', ')}</p>{/if}
		{#if value.inputs?.length}
			<ul class="inputs" aria-label="Rule inputs">
				{#each value.inputs as inp, i (i)}
					<li>
						<span><strong>{inp.group ?? 'Outside the rule'}</strong> · {inp.status.replaceAll('_', ' ')}{#if inp.selected} · selected{/if}</span>
						{#if inp.value != null}<span class="muted">{formatValue(inp.value, value.unit)}</span>{/if}
						{#if inp.reason}<span class="muted">{inp.reason}</span>{/if}
						{#each (inp.record_refs ?? []).slice(0, 3) as ref (ref)}
							<span class="record">
								<button class="btn link" type="button" onclick={() => (provenance = ref)}>Provenance<span class="visually-hidden"> of record {ref}</span></button>
								<button class="btn link" type="button" onclick={() => (override = { action: 'exclude_input', inputId: ref })}>
									Exclude<span class="visually-hidden"> record {ref}</span>
								</button>
							</span>
						{/each}
					</li>
				{/each}
			</ul>
		{/if}
	{:else}
		<p class="muted">No resolved value for this window.</p>
	{/if}
	<div class="actions">
		<button class="btn sm" type="button" onclick={() => (override = { action: 'exclude_input', inputId: '' })}>Exclude an input…</button>
		<button class="btn sm" type="button" disabled={!groups.length} onclick={() => (override = { action: 'force_source', inputId: '' })}>Force a source…</button>
		<button class="btn sm" type="button" onclick={() => (override = { action: 'set_value', inputId: '' })}>Set a value…</button>
		<a class="btn ghost sm" href="/explore/{encodeURIComponent(metric)}/day/{date}">All sources and overrides</a>
	</div>
</section>

{#if override}
	<OverrideDialog
		{metric}
		window={{ kind, key: date, local_date: date }}
		action={override.action}
		inputId={override.inputId}
		{groups}
		unit={value?.unit ?? ''}
		onsaved={() => {
			override = null;
			onchanged();
		}}
		onclose={() => (override = null)}
	/>
{/if}
{#if provenance}
	<ProvenanceDialog entity="measurement" id={provenance} onclose={() => (provenance = null)} />
{/if}

<style>
	.point {
		display: grid;
		gap: var(--space-2);
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
	}
	h2,
	p {
		margin: 0;
	}
	h2 {
		font-size: var(--text-md);
	}
	.headline {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: var(--space-3);
	}
	.value {
		font-size: var(--text-xl);
	}
	.warn {
		color: var(--color-warn);
	}
	.inputs {
		display: grid;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		font-size: var(--text-sm);
		list-style: none;
	}
	.inputs li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1) var(--space-3);
		align-items: baseline;
	}
	.record {
		display: inline-flex;
		gap: var(--space-2);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
</style>
