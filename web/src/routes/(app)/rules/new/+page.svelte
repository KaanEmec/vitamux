<!--
	Guided rule builder (frontend.md#rule-builder): metric → sources → strategy → window and
	quality → review with a 14-day preview. It writes the typed rule JSON
	(schemas/resolution-rule.v1.json); the server validates it on save and its field errors are
	shown by the inputs. Query: ?metric=<code> starts from the rule in effect, &from=<n> from
	version n, &blank=1 from an empty rule.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import PreviewTable from '#lib/rules/PreviewTable.svelte';
	import RuleDiff from '#lib/rules/RuleDiff.svelte';
	import SelectorList from '#lib/rules/SelectorList.svelte';
	import {
		blankForm,
		bucketSizes,
		fromSpec,
		groupKey,
		lastDays,
		needsSumAck,
		opLabel,
		ops,
		qualityFlags,
		selectorText,
		sumWarning,
		toSpec,
		windowKinds,
		windowLabel,
		type Form,
		type Rule,
		type SelectorField
	} from '#lib/rules/rule.ts';
	import { previewRule, type PreviewOutcome } from '#lib/rules/stubs.ts';

	const steps = ['Metric', 'Sources', 'Strategy', 'Window and quality', 'Review'];

	let active = $state<Schemas['RuleVersion'][]>([]);
	let metrics = $state<string[]>([]);
	let loadProblem = $state<Problem | null>(null);

	let metric = $state('');
	let startFrom = $state<'current' | 'blank'>('current');
	let fromVersion = $state<number | null>(null);
	let builtFrom = '';
	let form = $state<Form | null>(null);
	let step = $state(0);
	let heading = $state<HTMLElement>();

	let problem = $state<Problem | null>(null);
	let ackError = $state('');
	let note = $state('');
	let activate = $state(true);
	let saving = $state(false);
	let preview = $state<PreviewOutcome | 'loading' | null>(null);

	const errors = $derived(fieldErrors(problem));
	// Field errors that an input displays; ProblemAlert lists the rest.
	const shownBy =
		/^spec\.(groups(\.\d+(\.id|\.match\.\d+(\.\w+)?)?)?|exclude\.\d+(\.\w+)?|strategy\.min_sources|acknowledged_warnings|within_source\.span|quality\.(min_coverage|plausible_range(\.\d)?|max_staleness|require_wear)|follow)$/;
	const shown = $derived(Object.keys(errors).filter((k) => shownBy.test(k)));

	const activeRule = (m: string) => active.find((r) => r.metric === m);
	const currentSpec = $derived(form ? (activeRule(form.metric)?.spec as unknown as Rule | undefined) : undefined);
	const draft = $derived(form ? toSpec(form) : null);

	// Values seen in the rules in effect, offered as suggestions in the selector inputs.
	const suggestions = $derived.by(() => {
		const out: Partial<Record<SelectorField, string[]>> = {};
		for (const r of active) {
			const spec = r.spec as unknown as Rule;
			for (const s of [...spec.groups.flatMap((g) => g.match), ...(spec.exclude ?? [])]) {
				for (const [k, v] of Object.entries(s)) {
					if (typeof v !== 'string') continue;
					const list = (out[k as SelectorField] ??= []);
					if (!list.includes(v)) list.push(v);
				}
			}
		}
		return out;
	});

	onMount(async () => {
		const [rules, cat] = await Promise.all([api.GET('/api/v1/rules'), api.GET('/api/v1/metrics')]);
		if (rules.error) {
			loadProblem = rules.error;
			return;
		}
		active = rules.data.rules;
		const codes = active.map((r) => r.metric);
		for (const m of (cat.data?.metrics ?? []) as { code?: string }[]) if (m.code && !codes.includes(m.code)) codes.push(m.code);
		metrics = codes;

		const q = page.url.searchParams;
		const m = q.get('metric');
		if (m) {
			metric = m;
			startFrom = q.get('blank') ? 'blank' : 'current';
			fromVersion = q.get('from') ? Number(q.get('from')) : null;
			if (await build()) await go(1);
		}
	});

	/** Builds the form from the chosen metric and starting point; false if loading failed. */
	async function build(): Promise<boolean> {
		const key = `${metric}|${startFrom}|${fromVersion}`;
		if (form && key === builtFrom) return true;
		loadProblem = null;
		if (startFrom === 'blank') {
			form = blankForm(metric);
		} else if (fromVersion) {
			const { data, error } = await api.GET('/api/v1/rules/{metric}/versions', { params: { path: { metric } } });
			const v = data?.versions.find((x) => x.version === fromVersion);
			if (error || !v) {
				loadProblem = error ?? { type: 'about:blank', title: 'Not found', status: 404, code: 'not_found', detail: `No version ${fromVersion} of ${metric}.` };
				return false;
			}
			form = fromSpec(v.spec as unknown as Rule);
		} else {
			const r = activeRule(metric);
			form = r ? fromSpec(r.spec as unknown as Rule) : blankForm(metric);
		}
		builtFrom = key;
		problem = null;
		preview = null;
		return true;
	}

	async function go(n: number) {
		if (n > 0 && !(await build())) return;
		step = n;
		if (n === 4) void runPreview();
		await tick();
		heading?.focus();
	}

	async function runPreview() {
		if (!form) return;
		preview = 'loading';
		const { start, end } = lastDays(14);
		preview = await previewRule(toSpec(form), start, end);
	}

	function stepFor(key: string): number {
		if (/^spec\.metric/.test(key)) return 0;
		if (/^spec\.(groups|exclude)/.test(key)) return 1;
		if (/^spec\.(strategy|within_source|acknowledged_warnings)/.test(key)) return 2;
		if (/^spec\.(window|quality|follow|compose|contexts)/.test(key)) return 3;
		return 4;
	}
	const stepHasErrors = (n: number) => Object.keys(errors).some((k) => stepFor(k) === n) || (n === 2 && !!ackError);

	function moveGroup(i: number, by: number) {
		if (!form) return;
		const g = form.groups;
		[g[i], g[i + by]] = [g[i + by], g[i]];
	}

	function toggleAck(on: boolean) {
		if (!form) return;
		form.acknowledged = on ? [...form.acknowledged, sumWarning] : form.acknowledged.filter((w) => w !== sumWarning);
		if (on) ackError = '';
	}

	// Suggest excluding Apple Health relays when a group names a provider connected directly.
	const relaySuggestion = $derived(
		!!form &&
			form.groups.some((g) => g.match.some((s) => typeof s.provider === 'string' && s.provider && s.provider !== 'apple_health')) &&
			!form.exclude.some((s) => s.provider === 'apple_health' && s.relayed === true)
	);

	async function save() {
		if (!form) return;
		if (needsSumAck(form) && !form.acknowledged.includes(sumWarning)) {
			ackError = 'Confirm that you understand the duplicate risk before saving a sum.';
			await go(2);
			return;
		}
		saving = true;
		problem = null;
		const { data, error } = await api.POST('/api/v1/rules/{metric}/versions', {
			params: { path: { metric: form.metric } },
			body: { spec: toSpec(form) as unknown as Record<string, never>, note: note.trim() || undefined, activate }
		});
		saving = false;
		if (error) {
			problem = error;
			if (error.code === 'rule_warning_unacknowledged') {
				ackError = error.detail ?? 'Acknowledge the duplicate risk.';
				await go(2);
				return;
			}
			const first = Object.keys(fieldErrors(error))[0];
			if (first) await go(stepFor(first));
			return;
		}
		await goto(`/rules/${data.metric}?saved=${data.version}`);
	}
</script>

<svelte:head><title>Rule builder · Vitamux</title></svelte:head>

<p class="crumb"><a href="/rules">Rules</a> /</p>
<h1>Rule builder</h1>

<nav aria-label="Builder steps">
	<ol class="steps">
		{#each steps as label, i (label)}
			<li>
				<button
					type="button"
					class="step"
					aria-current={step === i ? 'step' : undefined}
					disabled={i > 0 && !form}
					onclick={() => go(i)}
				>
					<span class="num">{i + 1}</span>
					{label}
					{#if stepHasErrors(i)}<StatusIcon status="error" label="has errors" />{/if}
				</button>
			</li>
		{/each}
	</ol>
</nav>

<ProblemAlert problem={loadProblem} />
<ProblemAlert {problem} fields={shown} />

<section class="card" aria-labelledby="step-title">
	<h2 id="step-title" tabindex="-1" bind:this={heading}>{step + 1}. {steps[step]}</h2>

	{#if step === 0}
		<div class="field">
			<label for="metric">Metric</label>
			<select id="metric" bind:value={metric}>
				<option value="" disabled>Choose a metric…</option>
				{#each metrics as m (m)}<option value={m}>{m}</option>{/each}
			</select>
		</div>
		<fieldset class="choices">
			<legend>Start from</legend>
			<label><input type="radio" name="start" value="current" bind:group={startFrom} /> The rule in effect (reorder or adjust it)</label>
			<label><input type="radio" name="start" value="blank" bind:group={startFrom} /> An empty rule (replace it)</label>
		</fieldset>
		{#if metric && activeRule(metric)}
			{@const r = activeRule(metric)}
			<p class="muted">
				In effect: {r?.builtin ? 'built-in default' : `version ${r?.version}`}.
				{#if r?.reason}{r.reason}{/if}
			</p>
		{/if}
	{:else if form && step === 1}
		<p class="muted">
			Inputs join the first group they match, top to bottom. With "first source with data", the order is the fallback
			ladder.
		</p>
		{#if errors['spec.groups']}<p class="error">{errors['spec.groups']}</p>{/if}
		{#each form.groups as g, i (g.key)}
			<fieldset class="group">
				<legend>Group {i + 1}</legend>
				<div class="group-head">
					<TextField label="Group id" name="spec.groups.{i}.id" bind:value={g.id} error={errors[`spec.groups.${i}.id`]} hint="Lowercase letters, digits and _" />
					<div class="order">
						<button class="btn" type="button" disabled={i === 0} onclick={() => moveGroup(i, -1)} aria-label="Move group {i + 1} up">↑</button>
						<button class="btn" type="button" disabled={i === form.groups.length - 1} onclick={() => moveGroup(i, 1)} aria-label="Move group {i + 1} down">↓</button>
						<button class="btn" type="button" disabled={form.groups.length === 1} onclick={() => form?.groups.splice(i, 1)} aria-label="Remove group {i + 1}">Remove</button>
					</div>
				</div>
				{#if errors[`spec.groups.${i}`]}<p class="error">{errors[`spec.groups.${i}`]}</p>{/if}
				<SelectorList bind:list={g.match} kind="Match" errorPrefix="spec.groups.{i}.match" {errors} {suggestions} />
			</fieldset>
		{/each}
		<button class="btn add" type="button" onclick={() => form?.groups.push({ key: groupKey(), id: '', match: [{ provider: '' }] })}>Add group</button>

		<fieldset class="group">
			<legend>Exclusions</legend>
			<p class="muted">Inputs matching any exclusion are never used, whatever group they match.</p>
			{#if relaySuggestion}
				<p class="suggest">
					<StatusIcon status="info" /> A group names a provider that may also relay into Apple Health.
					<button class="btn" type="button" onclick={() => form?.exclude.push({ provider: 'apple_health', relayed: true })}>Exclude Apple Health relays</button>
				</p>
			{/if}
			<SelectorList bind:list={form.exclude} kind="Exclusion" errorPrefix="spec.exclude" {errors} {suggestions} />
		</fieldset>
	{:else if form && step === 2}
		<fieldset class="ops">
			<legend>How to combine the groups</legend>
			{#each ops as o (o.op)}
				<label class="op">
					<input type="radio" name="op" value={o.op} bind:group={form.op} />
					<span><strong>{o.label}</strong><br /><span class="muted">{o.hint}</span></span>
				</label>
			{/each}
		</fieldset>
		{#if form.op === 'single_source' && form.groups.length > 1}
			<p class="note"><StatusIcon status="warn" /> One source only needs exactly one group; remove the others in step 2.</p>
		{/if}
		{#if ops.find((o) => o.op === form?.op)?.pooling}
			<div class="row">
				<TextField label="Minimum sources" name="spec.strategy.min_sources" inputmode="numeric" bind:value={form.minSources} error={errors['spec.strategy.min_sources']} hint="Default 1" />
				<div class="field">
					<label for="insufficient">With fewer sources</label>
					<select id="insufficient" bind:value={form.onInsufficient}>
						<option value="">Default (use what is available)</option>
						<option value="use_available">Use what is available, with a warning</option>
						<option value="no_value">No value</option>
					</select>
				</div>
			</div>
		{/if}
		<fieldset class="group">
			<legend>Within each group</legend>
			<div class="row">
				<div class="field">
					<label for="intra">Several sources in one group</label>
					<select id="intra" bind:value={form.intraGroup}>
						<option value="">Default</option>
						<option value="auto">Automatic (mean, or max for counts)</option>
						<option value="mean">Mean</option>
						<option value="max">Highest</option>
						<option value="sum">Add them (duplicate risk)</option>
					</select>
				</div>
				<div class="field">
					<label for="dvp">Daily totals</label>
					<select id="dvp" bind:value={form.dailyValuePolicy}>
						<option value="">Default</option>
						<option value="prefer_reported">Prefer the provider's daily total</option>
						<option value="intervals_only">Only add up intervals</option>
					</select>
				</div>
				<div class="field">
					<label for="stat">Statistic</label>
					<select id="stat" bind:value={form.statistic}>
						<option value="">Default</option>
						<option value="latest">Latest reading of the day</option>
						<option value="mean">Mean of the day's readings</option>
						<option value="min">Lowest bucket</option>
						<option value="min_rolling_mean">Lowest rolling mean</option>
					</select>
				</div>
				{#if form.statistic === 'min_rolling_mean'}
					<TextField label="Rolling span" name="spec.within_source.span" bind:value={form.span} error={errors['spec.within_source.span']} hint="e.g. 30m" />
				{/if}
			</div>
		</fieldset>
		{#if needsSumAck(form)}
			<div class="ack" role="group" aria-labelledby="ack-title">
				<p id="ack-title"><StatusIcon status="warn" /> <strong>Adding sources can count the same activity twice.</strong></p>
				<p class="muted">
					If two devices recorded the same steps, a sum doubles them. Every result will carry this warning.
				</p>
				<label>
					<input
						type="checkbox"
						checked={form.acknowledged.includes(sumWarning)}
						onchange={(e) => toggleAck(e.currentTarget.checked)}
						aria-invalid={ackError || errors['spec.acknowledged_warnings'] ? 'true' : undefined}
					/>
					I understand the duplicate risk
				</label>
				{#if ackError || errors['spec.acknowledged_warnings']}
					<p class="error">{ackError || errors['spec.acknowledged_warnings']}</p>
				{/if}
			</div>
		{/if}
	{:else if form && step === 3}
		<div class="row">
			<div class="field">
				<label for="window">Window</label>
				<select id="window" bind:value={form.windowKind}>
					{#each windowKinds as w (w.kind)}<option value={w.kind}>{w.label}</option>{/each}
				</select>
				<span class="hint">The server checks that the metric allows it.</span>
				{#if errors['spec.window'] || errors['spec.window.kind']}<span class="error">{errors['spec.window'] || errors['spec.window.kind']}</span>{/if}
			</div>
			{#if form.windowKind === 'bucket'}
				<div class="field">
					<label for="size">Bucket size</label>
					<select id="size" bind:value={form.bucketSize}>
						{#each bucketSizes as s (s)}<option value={s}>{s}</option>{/each}
					</select>
				</div>
			{/if}
		</div>
		<fieldset class="group">
			<legend>Quality gates</legend>
			<div class="row">
				<TextField label="Minimum coverage" name="spec.quality.min_coverage" inputmode="decimal" bind:value={form.minCoverage} error={errors['spec.quality.min_coverage']} hint="0–1, e.g. 0.5" />
				<TextField label="Plausible low" name="spec.quality.plausible_range.0" inputmode="decimal" bind:value={form.rangeLow} error={errors['spec.quality.plausible_range.0'] || errors['spec.quality.plausible_range']} />
				<TextField label="Plausible high" name="spec.quality.plausible_range.1" inputmode="decimal" bind:value={form.rangeHigh} error={errors['spec.quality.plausible_range.1']} />
				<TextField label="Maximum staleness" name="spec.quality.max_staleness" bind:value={form.maxStaleness} error={errors['spec.quality.max_staleness']} hint="e.g. 36h or 30d" />
				<TextField label="Count only while worn (wear metric)" name="spec.quality.require_wear" bind:value={form.requireWear} error={errors['spec.quality.require_wear']} hint="e.g. heart_rate" />
			</div>
			<fieldset class="choices">
				<legend>Ignore inputs flagged as</legend>
				{#each qualityFlags as f (f)}
					<label><input type="checkbox" value={f} bind:group={form.excludeFlags} /> {f.replaceAll('_', ' ')}</label>
				{/each}
			</fieldset>
		</fieldset>
		<details class="group" open={!!(form.matchOverlap || form.minEpisodeCoverage || form.includeNaps || form.nightAnchor)}>
			<summary>Sleep alignment</summary>
			<div class="row">
				<TextField label="Episode match overlap" name="spec.quality.sleep.match_overlap" inputmode="decimal" bind:value={form.matchOverlap} hint="Default 0.5" />
				<TextField label="Minimum episode coverage" name="spec.quality.sleep.min_episode_coverage" inputmode="decimal" bind:value={form.minEpisodeCoverage} hint="Default 0.7" />
				<div class="field">
					<label for="naps">Naps</label>
					<select id="naps" bind:value={form.includeNaps}>
						<option value="">Default</option>
						<option value="false">Leave out naps</option>
						<option value="true">Include naps</option>
					</select>
				</div>
				<TextField label="Night anchor" name="spec.quality.sleep.night_anchor" bind:value={form.nightAnchor} hint="Default 18:00" />
			</div>
		</details>
		<details class="group" open={!!(form.follow || form.compose || form.contexts)}>
			<summary>Advanced</summary>
			<div class="row">
				<TextField label="Use the source another metric selected" name="spec.follow" bind:value={form.follow} error={errors['spec.follow']} hint="e.g. weight" />
				<div class="field">
					<label for="compose">Build the day from hourly picks</label>
					<select id="compose" bind:value={form.compose}>
						<option value="">No</option>
						<option value="first_available">Yes, first source with data per hour</option>
						<option value="max">Yes, highest source per hour</option>
					</select>
				</div>
			</div>
			{#if form.contexts}
				<p>
					Workout and sleep orderings are kept from the starting rule:
					<code>{JSON.stringify(form.contexts)}</code>
					<button class="btn link" type="button" onclick={() => form && (form.contexts = undefined)}>Remove them</button>
				</p>
			{/if}
		</details>
	{:else if form && draft && step === 4}
		<dl class="summary">
			<dt>Metric</dt><dd><code>{draft.metric}</code></dd>
			<dt>Window</dt><dd>{windowLabel(draft.window)}</dd>
			<dt>Strategy</dt><dd>{opLabel(draft.strategy.op)}</dd>
			<dt>Groups</dt>
			<dd>
				<ol>
					{#each draft.groups as g, i (i)}<li><code>{g.id}</code> <span class="muted">{g.match.map(selectorText).join(' or ')}</span></li>{/each}
				</ol>
			</dd>
			{#if draft.exclude?.length}<dt>Excluded</dt><dd>{draft.exclude.map(selectorText).join('; ')}</dd>{/if}
			{#if draft.acknowledged_warnings?.length}<dt>Acknowledged</dt><dd>{draft.acknowledged_warnings.join(', ')}</dd>{/if}
		</dl>
		<details>
			<summary>Rule JSON</summary>
			<pre>{JSON.stringify(draft, null, 2)}</pre>
		</details>
		{#if currentSpec}
			<details>
				<summary>Changes from the rule in effect</summary>
				<RuleDiff before={currentSpec} after={draft} caption="Rule in effect → draft" />
			</details>
		{/if}

		<h3>Preview: last 14 days</h3>
		{#if preview === 'loading'}
			<p class="muted" role="status">Resolving the draft…</p>
		{:else if preview && 'preview' in preview}
			<PreviewTable days={preview.preview.days} />
		{:else if preview && 'unavailable' in preview}
			<p class="note"><StatusIcon status="info" /> Preview unavailable: this server cannot resolve drafts yet. You can still save the rule.</p>
		{:else if preview && 'problem' in preview}
			<ProblemAlert problem={preview.problem} />
		{/if}
		<button class="btn" type="button" onclick={runPreview} disabled={preview === 'loading'}>Refresh preview</button>

		<div class="save">
			<TextField label="Note (optional)" name="note" bind:value={note} maxlength={500} />
			<label><input type="checkbox" bind:checked={activate} /> Make it the active rule</label>
		</div>
	{/if}

	<div class="nav">
		{#if step > 0}<button class="btn" type="button" onclick={() => go(step - 1)}>Back</button>{/if}
		{#if step < 4}
			<button class="btn primary" type="button" disabled={step === 0 && !metric} onclick={() => go(step + 1)}>Next</button>
		{:else}
			<button class="btn primary" type="button" disabled={saving} onclick={save}>{saving ? 'Saving…' : 'Save version'}</button>
		{/if}
	</div>
</section>

<style>
	.crumb {
		margin: 0;
		font-size: var(--text-sm);
	}
	.steps {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0 0 var(--space-4);
		padding: 0;
		list-style: none;
	}
	.step {
		display: inline-flex;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-1) var(--space-3);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: 999px;
		cursor: pointer;
	}
	.step[aria-current='step'] {
		font-weight: 600;
		border-color: var(--color-accent);
		box-shadow: inset 0 0 0 1px var(--color-accent);
	}
	.step:disabled {
		opacity: 0.5;
		cursor: not-allowed;
	}
	.num {
		font-weight: 700;
		color: var(--color-accent);
	}
	h2:focus {
		outline: none;
	}
	h3 {
		margin-top: var(--space-5);
	}
	.add {
		margin-bottom: var(--space-4);
	}
	fieldset {
		margin: 0 0 var(--space-4);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
		padding: var(--space-3) var(--space-4);
	}
	legend,
	summary {
		font-weight: 600;
	}
	details.group {
		margin-bottom: var(--space-4);
	}
	.choices {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-4);
	}
	.group-head,
	.row {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: flex-end;
	}
	.order {
		display: flex;
		gap: var(--space-2);
		margin-bottom: var(--space-4);
	}
	.ops {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
		gap: var(--space-2);
	}
	.op {
		display: flex;
		gap: var(--space-2);
		align-items: flex-start;
		padding: var(--space-2) var(--space-3);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
		cursor: pointer;
	}
	.op:has(input:checked) {
		border-color: var(--color-accent);
		box-shadow: inset 0 0 0 1px var(--color-accent);
	}
	.ack {
		padding: var(--space-3) var(--space-4);
		border: 1px solid var(--color-warn);
		border-radius: var(--radius-sm);
	}
	.ack p {
		margin: 0 0 var(--space-2);
	}
	.note,
	.suggest {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-2) var(--space-3);
		background: var(--color-info-bg);
		border-radius: var(--radius-sm);
	}
	.error {
		color: var(--color-error);
		font-size: var(--text-sm);
	}
	select {
		font: inherit;
		padding: var(--space-2) var(--space-3);
		color: var(--color-text);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.summary {
		display: grid;
		grid-template-columns: max-content 1fr;
		gap: var(--space-1) var(--space-4);
	}
	.summary dt {
		font-weight: 600;
	}
	.summary dd {
		margin: 0;
	}
	.summary ol {
		margin: 0;
		padding-left: var(--space-5);
	}
	pre {
		overflow: auto;
		padding: var(--space-3);
		font-size: var(--text-xs);
		background: var(--color-surface-2);
		border-radius: var(--radius-sm);
	}
	.save {
		margin-top: var(--space-5);
	}
	.nav {
		display: flex;
		justify-content: space-between;
		gap: var(--space-3);
		margin-top: var(--space-5);
	}
</style>
