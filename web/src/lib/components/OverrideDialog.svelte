<!--
	Creates a manual override for one metric window (POST /overrides): exclude one input,
	force a rule group, or set a value with a note. Overrides are audited and reversible
	(revoke); source rows are never changed (docs/architecture/resolution.md#manual-overrides).
-->
<script lang="ts" module>
	export type OverrideAction = 'exclude_input' | 'force_source' | 'set_value';
</script>

<script lang="ts">
	import { api, fieldErrors, type Problem, type Schemas } from '../api/client.ts';
	import { groupLabel } from '../rules/rule.ts';
	import Modal from './Modal.svelte';
	import ProblemAlert from './ProblemAlert.svelte';
	import TextField from './TextField.svelte';

	let {
		metric,
		window: win,
		action = 'exclude_input',
		inputId = '',
		groups = [],
		unit = '',
		onsaved,
		onclose
	}: {
		metric: string;
		window: Schemas['OverrideWindow'];
		action?: OverrideAction;
		inputId?: string;
		groups?: string[];
		unit?: string;
		onsaved: () => void;
		onclose: () => void;
	} = $props();

	// The props only seed the form; the user edits copies.
	// svelte-ignore state_referenced_locally
	let chosen = $state<OverrideAction>(action);
	// svelte-ignore state_referenced_locally
	let recordId = $state(inputId);
	// svelte-ignore state_referenced_locally
	let group = $state(groups[0] ?? '');
	let value = $state('');
	// svelte-ignore state_referenced_locally
	let valueUnit = $state(unit);
	let note = $state('');
	let problem = $state<Problem | null>(null);
	let busy = $state(false);

	const errors = $derived(fieldErrors(problem));
	const fields = ['input_id', 'group', 'value', 'unit', 'note'];
	const noteId = $props.id();

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = null;
		const body: Schemas['OverrideInput'] = { metric, window: win, action: chosen };
		if (chosen === 'exclude_input') body.input_id = recordId.trim();
		if (chosen === 'force_source') body.group = group;
		if (chosen === 'set_value') {
			body.value = Number(value);
			body.unit = valueUnit.trim();
			body.note = note.trim();
		}
		const res = await api.POST('/api/v1/overrides', { body });
		busy = false;
		if (res.error) {
			problem = res.error;
			return;
		}
		onsaved();
	}
</script>

<Modal title="Override {metric} for {win.local_date}" {onclose}>
	<form onsubmit={submit}>
		<ProblemAlert {problem} {fields} />
		<fieldset>
			<legend>Action</legend>
			<label><input type="radio" name="action" value="exclude_input" bind:group={chosen} /> Exclude an input</label>
			<label><input type="radio" name="action" value="force_source" bind:group={chosen} /> Force a source</label>
			<label><input type="radio" name="action" value="set_value" bind:group={chosen} /> Set a value</label>
		</fieldset>

		{#if chosen === 'exclude_input'}
			<TextField
				label="Record id"
				name="input_id"
				bind:value={recordId}
				error={errors.input_id}
				hint="The id of a measurement listed under Rule inputs. The window is resolved again without it."
				inputmode="numeric"
				required
			/>
		{:else if chosen === 'force_source'}
			<div class="field">
				<label for="{noteId}-group">Source group</label>
				<select id="{noteId}-group" name="group" bind:value={group} required aria-invalid={errors.group ? 'true' : undefined}>
					{#each groups as g (g)}<option value={g}>{groupLabel(g)}</option>{/each}
				</select>
				<span class="hint">Used if it has a value in this window.</span>
				{#if errors.group}<span class="error">{errors.group}</span>{/if}
			</div>
		{:else}
			<TextField label="Value" name="value" bind:value error={errors.value} type="number" step="any" required />
			<TextField label="Unit" name="unit" bind:value={valueUnit} error={errors.unit} required />
			<div class="field">
				<label for={noteId}>Note</label>
				<textarea id={noteId} name="note" bind:value={note} maxlength="1000" rows="3" required></textarea>
				<span class="hint">Why this value is set. It is stored with the override.</span>
				{#if errors.note}<span class="error">{errors.note}</span>{/if}
			</div>
		{/if}

		<div class="actions">
			<button class="btn primary" type="submit" disabled={busy}>Save override</button>
			<button class="btn" type="button" onclick={onclose}>Cancel</button>
		</div>
	</form>
</Modal>

<style>
	form {
		margin-top: var(--space-3);
	}
	fieldset {
		display: grid;
		gap: var(--space-2);
		margin: 0 0 var(--space-4);
		padding: var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	legend {
		font-weight: 600;
		font-size: var(--text-sm);
	}
	select,
	textarea {
		padding: var(--space-2) var(--space-3);
		font: inherit;
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.actions {
		display: flex;
		gap: var(--space-2);
	}
</style>
