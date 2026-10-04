<!--
	Edits a list of selectors (ORed); each selector is a set of conditions (ANDed), as in
	resolution.md#selectors-and-validation. Field errors use keys like "spec.groups.0.match.1".
-->
<script lang="ts">
	import type { Chip } from './chips.ts';
	import { selectorFields, selectorLabels, type Selector, type SelectorField } from './rule.ts';

	let {
		list = $bindable(),
		kind,
		errorPrefix,
		errors,
		suggestions,
		chips = []
	}: {
		list: Selector[];
		kind: string;
		errorPrefix: string;
		errors: Record<string, string>;
		suggestions: Partial<Record<SelectorField, string[]>>;
		/** One-click selectors (origins, device types, relayed or direct). */
		chips?: Chip[];
	} = $props();

	const uid = $props.id();

	function errorFor(si: number, field?: string): string {
		const base = `${errorPrefix}.${si}`;
		return field ? (errors[`${base}.${field}`] ?? '') : (errors[base] ?? '');
	}

	function addCondition(si: number) {
		const used = Object.keys(list[si]);
		const field = selectorFields.find((f) => !used.includes(f));
		if (field) list[si][field] = field === 'relayed' ? false : '';
	}

	function changeField(si: number, from: string, to: SelectorField) {
		list[si] = Object.fromEntries(
			Object.entries(list[si]).map(([k, v]) => (k === from ? [to, to === 'relayed' ? false : ''] : [k, v]))
		);
	}

	function removeCondition(si: number, field: string) {
		const next = { ...list[si] };
		delete next[field as SelectorField];
		list[si] = next;
	}
</script>

{#each Object.entries(suggestions) as [field, values] (field)}
	<datalist id="{uid}-{field}">
		{#each values as v (v)}<option value={v}></option>{/each}
	</datalist>
{/each}

{#each list as sel, si (si)}
	<fieldset class="selector">
		<legend>{kind} {si + 1}</legend>
		{#if errorFor(si)}<p class="error">{errorFor(si)}</p>{/if}
		{#each Object.keys(sel) as field, ci (field)}
			<div class="condition">
				<select
					aria-label="Condition {ci + 1} field"
					value={field}
					onchange={(e) => changeField(si, field, e.currentTarget.value as SelectorField)}
				>
					{#each selectorFields as f (f)}
						{#if f === field || !(f in sel)}<option value={f}>{selectorLabels[f]}</option>{/if}
					{/each}
				</select>
				{#if field === 'relayed'}
					<select aria-label="Condition {ci + 1} value" bind:value={sel.relayed}>
						<option value={false}>not relayed (direct)</option>
						<option value={true}>relayed by another app</option>
					</select>
				{:else if field === 'entry'}
					<select aria-label="Condition {ci + 1} value" bind:value={sel.entry}>
						<option value="device">measured by a device</option>
						<option value="manual">entered manually</option>
					</select>
				{:else}
					<input
						aria-label="Condition {ci + 1} value"
						list={field in suggestions ? `${uid}-${field}` : undefined}
						aria-invalid={errorFor(si, field) ? 'true' : undefined}
						bind:value={sel[field as SelectorField]}
					/>
				{/if}
				<button
					class="btn link"
					type="button"
					onclick={() => removeCondition(si, field)}
					aria-label="Remove condition {ci + 1} from {kind.toLowerCase()} {si + 1}">Remove</button
				>
				{#if errorFor(si, field)}<span class="error">{errorFor(si, field)}</span>{/if}
			</div>
		{/each}
		<div class="row">
			{#if Object.keys(sel).length < selectorFields.length}
				<button class="btn" type="button" onclick={() => addCondition(si)}>And…</button>
			{/if}
			<button class="btn link" type="button" onclick={() => list.splice(si, 1)}>Remove {kind.toLowerCase()} {si + 1}</button>
		</div>
	</fieldset>
{/each}
<button class="btn" type="button" onclick={() => list.push({ provider: '' })}>
	{list.length ? `Or ${kind.toLowerCase()}…` : `Add ${kind.toLowerCase()}`}
</button>
{#if chips.length}
	<div class="chips" role="group" aria-label="Add a {kind.toLowerCase()} from your sources">
		{#each chips as c (c.label)}
			<button class="chip" type="button" onclick={() => list.push({ ...c.selector })}>+ {c.label}</button>
		{/each}
	</div>
{/if}

<style>
	.selector {
		margin: 0 0 var(--space-2);
		padding: var(--space-2) var(--space-3);
		border: 1px dashed var(--color-border);
		border-radius: var(--radius-sm);
	}
	legend {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.condition,
	.row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin-bottom: var(--space-2);
	}
	select,
	input {
		font: inherit;
		padding: var(--space-1) var(--space-2);
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	input[aria-invalid='true'] {
		border-color: var(--color-error);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin-top: var(--space-2);
	}
	.chip {
		padding: var(--space-1) var(--space-2);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text);
		background: var(--color-surface-2);
		border: 1px solid var(--color-border);
		border-radius: 999px;
		cursor: pointer;
	}
	.chip:hover {
		border-color: var(--color-accent);
	}
	.error {
		margin: 0;
		color: var(--color-error);
		font-size: var(--text-sm);
	}
</style>
