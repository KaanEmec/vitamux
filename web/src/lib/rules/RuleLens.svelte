<!--
	Rule lens (J21.10, restyled in J23.7): change how a metric is resolved while looking at its data.
	Shows the rule in effect as a sentence, lets the owner reorder its source groups (drag, or the
	up and down buttons; with the windows each supplied when the page passes `counts`), edit
	exclusions, the strategy, the window and the minimum coverage (only what GET /metrics/{code}
	allows), and acknowledge the sum warning. Every change previews the draft over start..end
	(POST /resolution/preview, debounced): a draft-vs-active mini chart and the change summary, and
	the per-day draft goes to `ondraft` so the page can draw it as a ghost series. Save, save and
	activate, revert and history use /rules/{metric}/versions and /activate. A side panel; a bottom
	sheet under 48rem unless `inline` (the page stacks it instead).
-->
<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { api, fieldErrors, type Problem, type Schemas } from '../api/client.ts';
	import Modal from '../components/Modal.svelte';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import StatusIcon from '../components/StatusIcon.svelte';
	import TextField from '../components/TextField.svelte';
	import { addDays, formatValue } from '../data/format.ts';
	import Badge from '../ui/Badge.svelte';
	import EmptyState from '../ui/EmptyState.svelte';
	import { sourceClass } from '../ui/source.ts';
	import { selectorChips, sourceChoices, type Chip } from './chips.ts';
	import { dayChanged, numeric, selectedGroup, showResolved, summarize } from './preview.ts';
	import {
		bucketSizes,
		fromSpec,
		groupLabel,
		needsSumAck,
		ops,
		selectorKey,
		selectorText,
		sumWarning,
		toSpec,
		windowKinds,
		type Form,
		type Rule
	} from './rule.ts';
	import { opShort, ruleSentence } from './sentence.ts';
	import { previewRule, type PreviewOutcome } from './stubs.ts';

	type Version = Schemas['RuleVersion'];
	type Draft = { local_date: string; value: number | null; changed: boolean }[] | null;

	let {
		metric,
		start,
		end,
		ondraft,
		history = true,
		inline = false,
		counts,
		onsaved
	}: {
		metric: string;
		/** Local dates, inclusive; the preview covers at most the last 366 of them. */
		start: string;
		end: string;
		ondraft: (draft: Draft) => void;
		/** Show the version timeline (off where the page lists versions itself). */
		history?: boolean;
		/** Always a panel, never a bottom sheet (the page stacks it on small screens). */
		inline?: boolean;
		/** Windows each rule group supplied in the page's range, by group id. */
		counts?: Record<string, number>;
		/** After a save or an activation. */
		onsaved?: () => void;
	} = $props();

	const uid = $props.id();
	const title = 'How this is calculated';

	let def = $state<Schemas['Metric'] | null>(null);
	let versions = $state<Version[] | null>(null);
	let loadProblem = $state<Problem | null>(null);
	let chips = $state<Chip[]>([]); // one-click exclusions: the named choices, then the origins and device types
	let choices = $state<Chip[]>([]);

	let base = $state<Version | null>(null);
	let baseJson = '';
	let baseIds = $state<string[]>([]);
	let form = $state<Form | null>(null);

	let preview = $state<PreviewOutcome | 'loading' | null>(null);
	let problem = $state<Problem | null>(null);
	let ackError = $state('');
	let note = $state('');
	let busy = $state(false);
	let message = $state('');
	let revertTo = $state<number | null>(null);
	let moved = $state('');
	let root = $state<HTMLElement>();
	let ackInput = $state<HTMLInputElement>();

	const narrow = new MediaQuery('max-width: 48rem');
	let open = $state(false);

	const spec = (v: Version) => v.spec as unknown as Rule;
	const draft = $derived(form ? toSpec(form) : null);
	const dirty = $derived(!!draft && JSON.stringify(draft) !== baseJson);
	const nextVersion = $derived(Math.max(1, ...(versions ?? []).filter((v) => !v.builtin).map((v) => v.version)) + 1);

	const allowedOps = $derived(ops.filter((o) => !def || def.strategies.includes(o.op) || o.op === form?.op));
	const allowedWindows = $derived(
		windowKinds.filter((w) => !def || (def.windows as string[]).includes(w.kind) || w.kind === form?.windowKind)
	);
	const coverage = $derived(Math.round(Number(form?.minCoverage || 0) * 100) || 0);
	const exclusionChips = $derived(
		[...choices, ...chips].filter((c) => !form?.exclude.some((s) => selectorKey(s) === selectorKey(c.selector)))
	);

	// Field errors from a save or a preview, shown by their control; ProblemAlert lists the rest.
	const shownProblem = $derived(problem ?? (preview && preview !== 'loading' && 'problem' in preview ? preview.problem : null));
	const errors = $derived(fieldErrors(shownProblem));
	const controls = ['spec.groups', 'spec.exclude', 'spec.strategy', 'spec.window', 'spec.quality.min_coverage', 'spec.acknowledged_warnings'];
	const owns = (prefix: string, key: string) => key === prefix || key.startsWith(prefix + '.');
	const errorFor = (prefix: string) =>
		Object.entries(errors)
			.filter(([k]) => owns(prefix, k))
			.map(([, v]) => v)
			.join('; ');
	const shown = $derived(Object.keys(errors).filter((k) => controls.some((c) => owns(c, k))));

	$effect(() => {
		const m = metric;
		untrack(() => void load(m));
	});

	async function load(m: string) {
		versions = null;
		loadProblem = null;
		message = '';
		revertTo = null;
		void Promise.all([api.GET('/api/v1/origins'), api.GET('/api/v1/source-devices'), api.GET('/api/v1/providers')]).then(([o, d, p]) => {
			chips = selectorChips(o.data?.origins ?? [], d.data?.devices ?? []);
			choices = sourceChoices(d.data?.devices ?? [], p.data?.providers ?? []);
		});
		const [d] = await Promise.all([api.GET('/api/v1/metrics/{code}', { params: { path: { code: m } } }), reload(m)]);
		def = d.data ?? null;
	}

	async function reload(m = metric) {
		const { data, error } = await api.GET('/api/v1/rules/{metric}/versions', { params: { path: { metric: m } } });
		versions = data?.versions ?? [];
		if (error && error.status !== 404) loadProblem = error;
		rebase();
	}

	/** Starts the draft again from the rule in effect. */
	function rebase() {
		base = versions?.find((v) => v.active) ?? null;
		form = base ? fromSpec(spec(base)) : null;
		baseJson = form ? JSON.stringify(toSpec(form)) : '';
		baseIds = base ? spec(base).groups.map((g) => g.id) : [];
		problem = null;
		ackError = '';
	}

	// The preview range: the requested dates, capped like the server at 366.
	const range = $derived.by(() => {
		const days = Math.round((Date.parse(end) - Date.parse(start)) / 86_400_000) + 1;
		return days > 366 ? { start: addDays(end, -365), end } : { start, end };
	});

	let seq = 0;
	$effect(() => {
		const [s, r, changed] = [draft, range, dirty];
		const n = ++seq;
		problem = null; // a field error belongs to the draft that was saved
		if (!s || !changed) {
			preview = null;
			untrack(() => ondraft(null));
			return;
		}
		const t = setTimeout(async () => {
			preview = 'loading';
			const out = await previewRule(s, r.start, r.end);
			if (n !== seq) return;
			preview = out;
			ondraft(
				'preview' in out
					? out.preview.days.map((d) => ({ local_date: d.local_date, value: numeric(d.draft.value), changed: dayChanged(d) }))
					: null
			);
		}, 350);
		return () => clearTimeout(t);
	});

	const summary = $derived(preview && preview !== 'loading' && 'preview' in preview ? summarize(preview.preview.days) : null);
	const signed = (v: number, unit: string) => `${v > 0 ? '+' : v < 0 ? '−' : '±'}${formatValue(Math.abs(v), unit)}`;

	async function move(from: number, to: number, focus?: 'up' | 'down') {
		if (!form || to < 0 || to >= form.groups.length || from === to) return;
		const [g] = form.groups.splice(from, 1);
		form.groups.splice(to, 0, g);
		moved = `${groupLabel(g.id)} moved to position ${to + 1} of ${form.groups.length}.`;
		if (!focus) return;
		await tick();
		const want = document.getElementById(`${uid}-${focus}-${g.key}`) as HTMLButtonElement | null;
		const other = document.getElementById(`${uid}-${focus === 'up' ? 'down' : 'up'}-${g.key}`);
		(want && !want.disabled ? want : other)?.focus();
	}

	let dragFrom = $state<number | null>(null);

	function addExclusion(label: string) {
		const c = [...choices, ...chips].find((x) => x.label === label);
		if (c && form) form.exclude.push({ ...c.selector });
	}

	function toggleAck(on: boolean) {
		if (!form) return;
		form.acknowledged = on ? [...form.acknowledged, sumWarning] : form.acknowledged.filter((w) => w !== sumWarning);
		if (on) ackError = '';
	}

	async function focusFirstError() {
		await tick();
		root?.querySelector<HTMLElement>('[aria-invalid="true"], .invalid input:checked')?.focus();
	}

	async function save(activate: boolean) {
		if (!form) return;
		message = '';
		if (needsSumAck(form) && !form.acknowledged.includes(sumWarning)) {
			ackError = 'Confirm that you understand the duplicate risk before saving a sum.';
			await tick();
			ackInput?.focus();
			return;
		}
		const prev = base;
		busy = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/rules/{metric}/versions', {
			params: { path: { metric } },
			body: { spec: toSpec(form) as unknown as Record<string, never>, note: note.trim() || undefined, activate }
		});
		busy = false;
		if (error) {
			problem = error;
			if (error.code === 'rule_warning_unacknowledged') ackError = error.detail ?? 'Acknowledge the duplicate risk.';
			await focusFirstError();
			return;
		}
		note = '';
		await reload();
		// The version that was active before; a built-in becomes the owner's version 1 on first save.
		const back = prev && (prev.builtin ? versions?.find((v) => v.based_on === prev.ref) : prev);
		revertTo = activate && back ? back.version : null;
		message = activate ? `Version ${data.version} is now active.` : `Saved version ${data.version}. The rule in effect is unchanged.`;
		onsaved?.();
	}

	async function activate(version: number, revert = false) {
		busy = true;
		problem = null;
		const { error } = await api.POST('/api/v1/rules/{metric}/activate', { params: { path: { metric } }, body: { version } });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		await reload();
		revertTo = null;
		message = revert ? `Reverted: version ${version} is active again.` : `Version ${version} is now active.`;
		onsaved?.();
	}

	const when = (v: Version) => (v.builtin ? 'shipped default' : v.created_at ? new Date(v.created_at).toLocaleDateString() : '');
	const groupSource = (g: Form['groups'][number]) => {
		const p = g.match.find((s) => typeof s.provider === 'string' && s.provider)?.provider;
		return sourceClass(typeof p === 'string' ? p : g.id);
	};
	const sheet = $derived(narrow.current && !inline);
	const activeVersion = $derived(versions?.find((v) => v.active));
	const previewDays = $derived(preview && preview !== 'loading' && 'preview' in preview ? preview.preview.days : []);
	const hint = $derived(
		[ops.find((o) => o.op === form?.op)?.hint, def && !def.strategies.includes('sum_across_sources') && "Sum isn't offered: this metric isn't additive."]
			.filter(Boolean)
			.join(' ')
	);
</script>

{#snippet body()}
	<div class="body" bind:this={root}>
		{#if versions === null}
			<p class="muted" role="status">Loading the rule…</p>
		{:else if !form || !draft}
			<ProblemAlert problem={loadProblem} />
			<EmptyState title="No rule for this metric yet" text="Only the all-sources view shows it until you pick a source.">
				<a class="btn sm" href="/rules/new?metric={metric}&amp;blank=1">Create a rule</a>
			</EmptyState>
		{:else}
			<div class="sentence">
				{#if dirty}<Badge tone="draft">Draft · not saved</Badge>{/if}
				<p>{ruleSentence(draft)}</p>
			</div>

			<ProblemAlert problem={shownProblem} fields={shown} />

			<section class="part" aria-labelledby="{uid}-order">
				<div class="part-head">
					<h3 id="{uid}-order" class="lbl">Source priority</h3>
					<span class="muted small">Drag, or use the arrows</span>
				</div>
				<ol class="groups" aria-labelledby="{uid}-order" aria-describedby={errorFor('spec.groups') ? `${uid}-groups-err` : undefined}>
					{#each form.groups as g, i (g.key)}
						<li
							class={['group', groupSource(g), dragFrom === i && 'dragging']}
							draggable="true"
							ondragstart={(e) => {
								dragFrom = i;
								e.dataTransfer?.setData('text/plain', g.id);
							}}
							ondragover={(e) => e.preventDefault()}
							ondrop={(e) => {
								e.preventDefault();
								if (dragFrom !== null) void move(dragFrom, i);
								dragFrom = null;
							}}
							ondragend={() => (dragFrom = null)}
						>
							<span class="grip" aria-hidden="true">⋮⋮</span>
							<span class="rank">{i + 1}</span>
							<span class="dot" aria-hidden="true"></span>
							<span class="who">
								<span class="name"><span class="dot" aria-hidden="true"></span>{groupLabel(g.id)}{#if baseIds.indexOf(g.id) !== i}<Badge tone="draft">moved</Badge>{/if}</span>
								<span class="muted small">{g.match.map((s) => selectorText(s, choices)).join(' or ')}</span>
							</span>
							{#if counts}<span class="count muted small">{counts[g.id] ?? 0} {counts[g.id] === 1 ? 'day' : 'days'}</span>{/if}
							<span class="arrows">
								<button id="{uid}-up-{g.key}" class="btn ghost sm icon-btn" type="button" disabled={i === 0} aria-label="Move {groupLabel(g.id)} up" onclick={() => move(i, i - 1, 'up')}>↑</button>
								<button id="{uid}-down-{g.key}" class="btn ghost sm icon-btn" type="button" disabled={i === form.groups.length - 1} aria-label="Move {groupLabel(g.id)} down" onclick={() => move(i, i + 1, 'down')}>↓</button>
							</span>
						</li>
					{/each}
				</ol>
				<p class="visually-hidden" aria-live="polite">{moved}</p>
				{#if errorFor('spec.groups')}<p class="error" id="{uid}-groups-err">{errorFor('spec.groups')}</p>{/if}

				<div class="excludes" role="group" aria-label="Never use">
					<span class="muted small">Never use:</span>
					{#each form.exclude as s, i (i)}
						<span class="chip">
							{selectorText(s, choices) || 'empty exclusion'}
							<button type="button" class="x" aria-label="Remove exclusion {selectorText(s, choices)}" onclick={() => form?.exclude.splice(i, 1)}>×</button>
						</span>
					{:else}
						<span class="muted small">nothing excluded</span>
					{/each}
					{#if exclusionChips.length}
						<select
							class="add"
							aria-label="Add an exclusion"
							value=""
							onchange={(e) => {
								addExclusion(e.currentTarget.value);
								e.currentTarget.value = '';
							}}
						>
							<option value="">+ Exclude a source…</option>
							{#each exclusionChips as c (c.label)}<option value={c.label}>{c.label}</option>{/each}
						</select>
					{/if}
				</div>
				{#if errorFor('spec.exclude')}<p class="error">{errorFor('spec.exclude')}</p>{/if}
			</section>

			<div class="row">
				<div class="field">
					<label class="lbl" for="{uid}-op">Strategy</label>
					<select
						id="{uid}-op"
						bind:value={form.op}
						aria-invalid={errorFor('spec.strategy') ? 'true' : undefined}
						aria-describedby="{uid}-op-hint{errorFor('spec.strategy') ? ` ${uid}-op-err` : ''}"
					>
						{#each allowedOps as o (o.op)}<option value={o.op}>{opShort[o.op]}</option>{/each}
					</select>
				</div>
				<div class="field">
					<label class="lbl" for="{uid}-window">Window</label>
					<select
						id="{uid}-window"
						bind:value={form.windowKind}
						aria-invalid={errorFor('spec.window') ? 'true' : undefined}
						aria-describedby={errorFor('spec.window') ? `${uid}-window-err` : undefined}
					>
						{#each allowedWindows as w (w.kind)}<option value={w.kind}>{w.label}</option>{/each}
					</select>
				</div>
				<p class="muted small wide" id="{uid}-op-hint">
					{hint}
				</p>
				{#if errorFor('spec.strategy')}<p class="error wide" id="{uid}-op-err">{errorFor('spec.strategy')}</p>{/if}
				{#if errorFor('spec.window')}<p class="error wide" id="{uid}-window-err">{errorFor('spec.window')}</p>{/if}
				{#if form.windowKind === 'bucket'}
					<div class="field">
						<label class="lbl" for="{uid}-size">Bucket size</label>
						<select id="{uid}-size" bind:value={form.bucketSize}>
							{#each bucketSizes as s (s)}<option value={s}>{s}</option>{/each}
						</select>
					</div>
				{/if}
				<div class="field wide">
					<label class="lbl" for="{uid}-cov">Minimum coverage · {coverage ? `${coverage}%` : 'none'}</label>
					<input
						id="{uid}-cov"
						type="range"
						min="0"
						max="100"
						step="5"
						value={coverage}
						oninput={(e) => form && (form.minCoverage = e.currentTarget.value === '0' ? '' : String(Number(e.currentTarget.value) / 100))}
						aria-invalid={errorFor('spec.quality.min_coverage') ? 'true' : undefined}
						aria-describedby={errorFor('spec.quality.min_coverage') ? `${uid}-cov-err` : undefined}
					/>
					{#if errorFor('spec.quality.min_coverage')}<span class="error" id="{uid}-cov-err">{errorFor('spec.quality.min_coverage')}</span>{/if}
				</div>
			</div>

			{#if needsSumAck(form)}
				<div class="inline-alert warn" role="group" aria-labelledby="{uid}-ack">
					<StatusIcon status="warn" />
					<div class="ack">
						<strong id="{uid}-ack">Adding sources can count the same activity twice.</strong>
						<label class="check">
							<input
								type="checkbox"
								bind:this={ackInput}
								checked={form.acknowledged.includes(sumWarning)}
								onchange={(e) => toggleAck(e.currentTarget.checked)}
								aria-invalid={ackError || errorFor('spec.acknowledged_warnings') ? 'true' : undefined}
								aria-describedby={ackError || errorFor('spec.acknowledged_warnings') ? `${uid}-ack-err` : undefined}
							/>
							I understand the duplicate risk
						</label>
						{#if ackError || errorFor('spec.acknowledged_warnings')}
							<p class="error" id="{uid}-ack-err">{ackError || errorFor('spec.acknowledged_warnings')}</p>
						{/if}
						</div>
				</div>
			{/if}

			<a class="small" href="/rules/new?metric={metric}">Plausible range, flags, staleness… open full builder</a>

			<section class={['preview', dirty && 'on']} aria-labelledby="{uid}-preview" aria-live="polite">
				<div class="part-head">
					<h3 id="{uid}-preview">Draft preview</h3>
					{#if dirty}<span class="muted small">vs active</span>{/if}
				</div>
				{#if !dirty}
					<p class="muted small">Change the rule to preview it on {range.start} to {range.end}.</p>
				{:else if preview === 'loading' || preview === null}
					<p class="muted small">Resolving the draft…</p>
				{:else if 'unavailable' in preview}
					<p class="inline-alert info"><StatusIcon status="info" /><span>Preview unavailable: this server cannot resolve drafts yet. You can still save the rule.</span></p>
				{:else if summary}
					{#await import('../charts/Sparkline.svelte') then { default: Sparkline }}
						<Sparkline
							ys={previewDays.map((d) => numeric(d.active.value))}
							ghost={previewDays.map((d) => numeric(d.draft.value))}
							label="Active rule (solid) and draft (dashed) over {previewDays.length} days"
						/>
					{/await}
					<p class="summary">
						<strong>{summary.changed.length} of {previewDays.length} days change</strong>
						{#if summary.shift !== null}· mean {signed(summary.shift, summary.unit)}{/if}
						· {summary.newGaps ? `${summary.newGaps} new ${summary.newGaps === 1 ? 'gap' : 'gaps'}` : 'no new gaps'}
					</p>
					{#if summary.changed.length}
						<details>
							<summary class="small">Days that change</summary>
							<div class="scroll">
								<table class="changes">
									<caption class="visually-hidden">Days that change</caption>
									<thead><tr><th scope="col">Day</th><th scope="col">Active</th><th scope="col">Draft</th><th scope="col">Why</th></tr></thead>
									<tbody>
										{#each summary.changed as d (d.local_date)}
											<tr>
												<th scope="row">{d.local_date}</th>
												<td>{showResolved(d.active)}{#if selectedGroup(d.active)}<span class="muted">{` · ${selectedGroup(d.active)}`}</span>{/if}</td>
												<td class="draft">{showResolved(d.draft)}{#if selectedGroup(d.draft)}<span class="muted">{` · ${selectedGroup(d.draft)}`}</span>{/if}</td>
												<td class="muted">{d.draft.explanation}</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</details>
					{/if}
				{/if}
			</section>

			<div class="save">
				<TextField label="Note (optional)" name="note" bind:value={note} maxlength={500} />
				<div class="buttons">
					<button class="btn primary" type="button" disabled={!dirty || busy} onclick={() => save(true)}>Save and activate</button>
					<button class="btn" type="button" disabled={!dirty || busy} onclick={() => save(false)}>Save as version {nextVersion}</button>
					<button class="btn ghost" type="button" disabled={!dirty || busy} onclick={rebase}>Discard</button>
				</div>
			</div>
		{/if}

		{#if message}
			<div class="inline-alert ok" role="status">
				<StatusIcon status="ok" />
				<span>{message}</span>
				{#if revertTo && !history}
					<div class="alert-actions">
						<button class="btn sm" type="button" disabled={busy} onclick={() => revertTo && activate(revertTo, true)}>Revert to version {revertTo}</button>
					</div>
				{/if}
			</div>
		{/if}

		{#if history && versions?.length}
			<section class="part history" aria-labelledby="{uid}-history">
				<h3 id="{uid}-history" class="lbl">History</h3>
				<ol class="timeline">
					{#each versions as v (v.ref)}
						<li class={{ active: v.active }}>
							<span class="mark" aria-hidden="true"></span>
							<span class="who">
								<span>{v.builtin ? 'Built-in' : `Version ${v.version}`}{v.note ? ` · ${v.note}` : ''}</span>
								<span class="muted small">{v.active ? 'Active' : ''}{v.active && when(v) ? ' · ' : ''}{when(v)}</span>
							</span>
							{#if !v.active && !v.builtin}
								{@const older = !!activeVersion && !activeVersion.builtin && v.version < activeVersion.version}
								<button class="btn link sm" type="button" disabled={busy} aria-label="{older ? 'Revert to' : 'Activate'} version {v.version}" onclick={() => activate(v.version, older)}>
									{older ? 'Revert' : 'Activate'}
								</button>
							{/if}
						</li>
					{/each}
				</ol>
			</section>
		{/if}
	</div>
{/snippet}

{#if sheet}
	<button class="btn open" type="button" onclick={() => (open = true)}>
		{title}{#if dirty}<Badge tone="draft">Draft</Badge>{/if}
	</button>
	{#if open}
		<Modal {title} drawer="right" onclose={() => (open = false)}>{@render body()}</Modal>
	{/if}
{:else}
	<aside class="lens card" aria-labelledby="{uid}-title">
		<div class="lens-head">
			<h2 id="{uid}-title">{title}</h2>
			{#if activeVersion}<span class="chip">{activeVersion.builtin ? 'Built-in' : `Rule v${activeVersion.version}`} · active</span>{/if}
		</div>
		{@render body()}
	</aside>
{/if}

<style>
	.lens {
		display: grid;
		gap: var(--space-4);
		min-width: 0;
	}
	.lens-head {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
	}
	.lens h2 {
		margin: 0;
		font-size: var(--text-md);
	}
	.open {
		width: 100%;
	}
	.body {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-5);
		min-width: 0;
	}
	.body > :global(*) {
		margin: 0;
	}
	.sentence {
		display: grid;
		justify-items: start;
		gap: var(--space-2);
	}
	.sentence p {
		margin: 0;
		font-size: var(--text-sm);
		line-height: 1.55;
		color: var(--color-text);
	}
	.part {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-2);
		min-width: 0;
	}
	.part-head {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
	}
	h3 {
		margin: 0;
		padding: 0;
		font-size: var(--text-sm);
		font-weight: 600;
	}
	.lbl {
		font-size: var(--text-2xs);
		font-weight: 500;
		letter-spacing: var(--tracking-label);
		text-transform: uppercase;
		color: var(--color-text-muted);
	}
	.small {
		font-size: var(--text-xs);
	}
	.groups,
	.timeline {
		display: grid;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.group {
		display: flex;
		align-items: center;
		gap: var(--space-2);
		padding: var(--space-2) var(--space-2) var(--space-2) var(--space-3);
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
		cursor: grab;
	}
	.group.dragging {
		border-color: var(--color-draft);
		box-shadow: var(--shadow-2);
	}
	.grip {
		color: var(--color-text-faint);
		letter-spacing: -0.2em;
	}
	.rank {
		flex: none;
		min-width: 1rem;
		font-family: var(--font-mono);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
		text-align: center;
	}
	.dot {
		flex: none;
		width: 0.4375rem;
		height: 0.4375rem;
		background: var(--src, var(--color-neutral));
		border-radius: 50%;
	}
	.who {
		display: grid;
		flex: 1;
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.name {
		display: inline-flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
		font-size: var(--text-sm);
		font-weight: 500;
	}
	.count {
		flex: none;
		font-variant-numeric: tabular-nums;
	}
	.arrows {
		display: flex;
		flex: none;
	}
	.excludes {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.x {
		width: 1.25rem;
		height: 1.25rem;
		margin-right: -0.375rem;
		padding: 0;
		font: inherit;
		color: var(--color-text-muted);
		background: none;
		border: 0;
		border-radius: 50%;
		cursor: pointer;
	}
	.x:hover {
		color: var(--color-text);
		background: var(--color-surface-2);
	}
	.add {
		max-width: 100%;
		min-height: var(--control-h-sm);
		font-size: var(--text-xs);
	}
	.ack {
		display: grid;
		gap: var(--space-2);
	}
	.row {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-2) var(--space-3);
	}
	.row .field {
		display: grid;
		gap: var(--space-1);
		min-width: 0;
		margin: 0;
	}
	.row .wide {
		grid-column: 1 / -1;
		margin: 0;
	}
	.row select {
		width: 100%;
		min-width: 0;
	}
	input[type='range'] {
		min-height: var(--control-h);
		accent-color: var(--color-accent);
	}
	.error {
		margin: 0;
		font-size: var(--text-xs);
		color: var(--color-error);
	}
	.preview {
		display: grid;
		gap: var(--space-2);
		min-width: 0;
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.preview.on {
		background: var(--color-draft-bg);
		border-color: color-mix(in srgb, var(--color-draft) 30%, transparent);
	}
	.preview.on h3 {
		color: var(--color-draft);
	}
	.preview > p {
		margin: 0;
	}
	.preview :global(.ghost) {
		stroke: var(--color-draft);
	}
	.summary {
		font-size: var(--text-sm);
		line-height: 1.45;
	}
	details summary {
		color: var(--color-link);
		cursor: pointer;
	}
	.scroll {
		max-height: 18rem;
		margin-top: var(--space-2);
		overflow: auto;
	}
	.changes {
		width: 100%;
		border-collapse: collapse;
		font-size: var(--text-xs);
	}
	.changes tbody th {
		white-space: nowrap;
	}
	.changes th,
	.changes td {
		padding: var(--space-2);
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--color-border);
	}
	.changes thead th {
		font-weight: 500;
		color: var(--color-text-muted);
	}
	.changes .draft {
		font-weight: 600;
	}
	.save {
		display: grid;
		gap: var(--space-2);
	}
	.save :global(.field) {
		margin: 0;
	}
	.buttons {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	.history {
		padding-top: var(--space-4);
		border-top: 1px solid var(--color-border);
	}
	.timeline li {
		display: flex;
		align-items: flex-start;
		gap: var(--space-3);
		font-size: var(--text-sm);
	}
	.mark {
		flex: none;
		width: 0.5rem;
		height: 0.5rem;
		margin-top: 0.4rem;
		border: 1.5px solid var(--color-text-faint);
		border-radius: 50%;
	}
	.timeline .active .mark {
		background: var(--color-accent);
		border-color: var(--color-accent);
	}
</style>
