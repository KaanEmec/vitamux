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
	import Notice from '#lib/settings/Notice.svelte';
	import Button from '#lib/ui/Button.svelte';
	import { selectorChips, seenValues, sourceChoices } from '#lib/rules/chips.ts';
	import PreviewTable from '#lib/rules/PreviewTable.svelte';
	import RuleDiff from '#lib/rules/RuleDiff.svelte';
	import SelectorList from '#lib/rules/SelectorList.svelte';
	import {
		blankForm,
		bucketSizes,
		fromSpec,
		groupKey,
		lastDays,
		groupLabel,
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
	import { dayChanged, numeric } from '#lib/rules/preview.ts';
	import { ruleSentence } from '#lib/rules/sentence.ts';
	import { previewRule, type PreviewOutcome } from '#lib/rules/stubs.ts';

	const steps = ['Metric', 'Sources', 'Strategy', 'Window and quality', 'Review'];

	let active = $state<Schemas['RuleVersion'][]>([]);
	let metrics = $state<string[]>([]);
	let loadProblem = $state<Problem | null>(null);
	// Origins and devices data came from, for one-click selectors; the builder works without them.
	let origins = $state<Schemas['DataOrigin'][]>([]);
	let sourceDevices = $state<Schemas['SourceDevice'][]>([]);
	let providers = $state<Schemas['Provider'][]>([]);
	const chips = $derived(selectorChips(origins, sourceDevices));
	const choices = $derived(sourceChoices(sourceDevices, providers));

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

	// Values seen in the data and in the rules in effect, offered as suggestions in the selector inputs.
	const suggestions = $derived.by(() => {
		const out: Partial<Record<SelectorField, string[]>> = seenValues(origins, sourceDevices);
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
		void Promise.all([api.GET('/api/v1/origins'), api.GET('/api/v1/source-devices'), api.GET('/api/v1/providers')]).then(([o, d, p]) => {
			origins = o.data?.origins ?? [];
			sourceDevices = d.data?.devices ?? [];
			providers = p.data?.providers ?? [];
		});
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

	// Live preview beside steps 2–4: the draft against the rule in effect, debounced.
	const live = $derived(!!form && step >= 1 && step <= 3);
	const baseJson = $derived(currentSpec ? JSON.stringify(toSpec(fromSpec(currentSpec))) : '');
	const changed = $derived(!!draft && JSON.stringify(draft) !== baseJson);
	let side = $state<PreviewOutcome | 'loading' | null>(null);
	let sideSeq = 0;
	$effect(() => {
		const [d, on] = [draft, live && changed];
		const n = ++sideSeq;
		if (!d || !on) {
			side = null;
			return;
		}
		const t = setTimeout(async () => {
			side = 'loading';
			const { start, end } = lastDays(14);
			const out = await previewRule(d, start, end);
			if (n === sideSeq) side = out;
		}, 400);
		return () => clearTimeout(t);
	});
	const sideDays = $derived(side && side !== 'loading' && 'preview' in side ? side.preview.days : null);
	const sideSeries = $derived.by(() => {
		if (!sideDays?.some((d) => numeric(d.draft.value) !== null || numeric(d.active.value) !== null)) return null;
		const xs = sideDays.map((d) => Date.parse(`${d.local_date}T12:00:00Z`));
		return [
			{ label: 'Rule in effect', xs, ys: sideDays.map((d) => numeric(d.active.value)) },
			{ label: 'Draft', xs, ys: sideDays.map((d) => numeric(d.draft.value)), style: 'ghost' as const }
		];
	});

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

<nav class="crumb" aria-label="Breadcrumb"><a href="/rules">Rules</a> <span aria-hidden="true">/</span>{#if form}<span>{` ${form.metric}`}</span>{/if}</nav>
<div class="head">
	<div>
		<h1>Rule builder</h1>
		{#if form}
			<p class="muted">
				{form.metric} · starting from {startFrom === 'blank' ? 'an empty rule' : fromVersion ? `version ${fromVersion}` : 'the rule in effect'}
			</p>
		{/if}
	</div>
	<Button variant="ghost" href={form ? `/rules/${form.metric}` : '/rules'}>Cancel</Button>
</div>

<nav aria-label="Builder steps">
	<ol class="steps">
		{#each steps as label, i (label)}
			<li>
				<button
					type="button"
					class={['btn step', form && i < step && 'done']}
					aria-current={step === i ? 'step' : undefined}
					disabled={i > 0 && !form}
					onclick={() => go(i)}
				>
					<span class="num" aria-hidden="true">{form && i < step ? '✓' : i + 1}</span>
					{label}
					{#if stepHasErrors(i)}<StatusIcon status="error" label="has errors" />{/if}
				</button>
			</li>
		{/each}
	</ol>
</nav>

<ProblemAlert problem={loadProblem} />
<ProblemAlert {problem} fields={shown} />

<div class={['layout', live && 'with-side']}>
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
			<label class="check"><input type="radio" name="start" value="current" bind:group={startFrom} /> The rule in effect (reorder or adjust it)</label>
			<label class="check"><input type="radio" name="start" value="blank" bind:group={startFrom} /> An empty rule (replace it)</label>
		</fieldset>
		{#if metric && activeRule(metric)}
			{@const r = activeRule(metric)}
			<p class="muted">
				In effect: {r?.default ? 'default rule' : r?.builtin ? 'built-in default' : `version ${r?.version}`}.
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
						<Button disabled={i === 0} onclick={() => moveGroup(i, -1)} aria-label="Move group {i + 1} up">↑</Button>
						<Button disabled={i === form.groups.length - 1} onclick={() => moveGroup(i, 1)} aria-label="Move group {i + 1} down">↓</Button>
						<Button disabled={form.groups.length === 1} onclick={() => form?.groups.splice(i, 1)} aria-label="Remove group {i + 1}">Remove</Button>
					</div>
				</div>
				{#if errors[`spec.groups.${i}`]}<p class="error">{errors[`spec.groups.${i}`]}</p>{/if}
				<SelectorList bind:list={g.match} kind="Match" errorPrefix="spec.groups.{i}.match" {errors} {suggestions} {chips} {choices} />
			</fieldset>
		{/each}
		<p class="add"><Button onclick={() => form?.groups.push({ key: groupKey(), id: '', match: [{ provider: '' }] })}>Add group</Button></p>

		<fieldset class="group">
			<legend>Exclusions</legend>
			<p class="muted">Inputs matching any exclusion are never used, whatever group they match.</p>
			{#if relaySuggestion}
				<div class="inline-alert info">
					<StatusIcon status="info" />
					<span>A group names a provider that may also relay into Apple Health.</span>
					<div class="alert-actions">
						<Button size="sm" onclick={() => form?.exclude.push({ provider: 'apple_health', relayed: true })}>Exclude Apple Health relays</Button>
					</div>
				</div>
			{/if}
			<SelectorList bind:list={form.exclude} kind="Exclusion" errorPrefix="spec.exclude" {errors} {suggestions} {chips} {choices} />
		</fieldset>
	{:else if form && step === 2}
		<fieldset class="ops">
			<legend>How to combine the groups</legend>
			{#each ops as o (o.op)}
				<label class="option-card">
					<input type="radio" name="op" value={o.op} bind:group={form.op} />
					<span><strong>{o.label}</strong><br /><span class="muted">{o.hint}</span></span>
				</label>
			{/each}
		</fieldset>
		{#if form.op === 'single_source' && form.groups.length > 1}
			<Notice status="warn">One source only needs exactly one group; remove the others in step 2.</Notice>
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
			<div class="inline-alert warn" role="group" aria-labelledby="ack-title">
				<StatusIcon status="warn" />
				<div class="ack">
					<p id="ack-title"><strong>Adding sources can count the same activity twice.</strong></p>
					<p class="muted">
						If two devices recorded the same steps, a sum doubles them. Every result will carry this warning.
					</p>
					<label class="check">
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
				<TextField label="Minimum coverage" name="spec.quality.min_coverage" inputmode="decimal" bind:value={form.minCoverage} error={errors['spec.quality.min_coverage']} hint="Opt-in, 0–1, e.g. 0.5. Empty: no gate" />
				<TextField label="Plausible low" name="spec.quality.plausible_range.0" inputmode="decimal" bind:value={form.rangeLow} error={errors['spec.quality.plausible_range.0'] || errors['spec.quality.plausible_range']} />
				<TextField label="Plausible high" name="spec.quality.plausible_range.1" inputmode="decimal" bind:value={form.rangeHigh} error={errors['spec.quality.plausible_range.1']} />
				<TextField label="Maximum staleness" name="spec.quality.max_staleness" bind:value={form.maxStaleness} error={errors['spec.quality.max_staleness']} hint="e.g. 36h or 30d" />
				<TextField label="Count only while worn (wear metric)" name="spec.quality.require_wear" bind:value={form.requireWear} error={errors['spec.quality.require_wear']} hint="Opt-in, e.g. heart_rate. Empty: no gate" />
			</div>
			<fieldset class="choices">
				<legend>Ignore inputs flagged as</legend>
				{#each qualityFlags as f (f)}
					<label class="check"><input type="checkbox" value={f} bind:group={form.excludeFlags} /> {f.replaceAll('_', ' ')}</label>
				{/each}
			</fieldset>
		</fieldset>
		<details class="group" open={!!(form.matchOverlap || form.minEpisodeCoverage || form.includeNaps || form.nightAnchor)}>
			<summary>Sleep alignment</summary>
			<div class="row">
				<TextField label="Episode match overlap" name="spec.quality.sleep.match_overlap" inputmode="decimal" bind:value={form.matchOverlap} hint="Default 0.5" />
				<TextField label="Minimum episode coverage" name="spec.quality.sleep.min_episode_coverage" inputmode="decimal" bind:value={form.minEpisodeCoverage} hint="Opt-in, e.g. 0.7. Empty: no gate" />
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
					{#each draft.groups as g, i (i)}<li><code>{groupLabel(g.id)}</code> <span class="muted">{g.match.map((s) => selectorText(s, choices)).join(' or ')}</span></li>{/each}
				</ol>
			</dd>
			{#if draft.exclude?.length}<dt>Excluded</dt><dd>{draft.exclude.map((s) => selectorText(s, choices)).join('; ')}</dd>{/if}
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
			<Notice status="info">Preview unavailable: this server cannot resolve drafts yet. You can still save the rule.</Notice>
		{:else if preview && 'problem' in preview}
			<ProblemAlert problem={preview.problem} />
		{/if}
		<Button onclick={runPreview} disabled={preview === 'loading'}>Refresh preview</Button>

		<div class="save">
			<TextField label="Note (optional)" name="note" bind:value={note} maxlength={500} />
			<label class="check"><input type="checkbox" bind:checked={activate} /> Make it the active rule</label>
		</div>
	{/if}

	<div class="nav">
		{#if step > 0}<Button onclick={() => go(step - 1)}>Back</Button>{/if}
		{#if step < 4}
			<Button variant="primary" disabled={step === 0 && !metric} onclick={() => go(step + 1)}>Next: {steps[step + 1].toLowerCase()}</Button>
		{:else}
			<Button variant="primary" disabled={saving} onclick={save}>{saving ? 'Saving…' : 'Save version'}</Button>
		{/if}
	</div>
</section>

{#if live && draft}
	<aside class="side card" aria-labelledby="live-title">
		<div class="side-head">
			<h2 id="live-title">Live preview</h2>
			<span class="muted small">Last 14 days</span>
		</div>
		<p class="sentence">{ruleSentence(draft)}</p>
		<div aria-live="polite">
			{#if !changed}
				<p class="muted small">No changes from the rule in effect yet.</p>
			{:else if side === null || side === 'loading'}
				<p class="muted small">Resolving the draft…</p>
			{:else if 'unavailable' in side}
				<Notice status="info">Preview unavailable on this server; the review step still checks the rule.</Notice>
			{:else if 'problem' in side}
				<p class="muted small">Not ready to preview: {side.problem.detail || side.problem.title}</p>
			{:else if sideDays}
				<p class="small"><strong>{sideDays.filter(dayChanged).length} of {sideDays.length} days differ</strong> from the rule in effect.</p>
			{/if}
		</div>
		{#if changed && sideSeries}
			{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
				<TimeSeries series={sideSeries} label="Rule in effect and draft, last 14 days" unit={sideDays?.[0]?.draft.unit ?? ''} height={180} zoom={false} withTime={false} />
			{/await}
		{/if}
		<details>
			<summary>Rule JSON and diff</summary>
			<pre>{JSON.stringify(draft, null, 2)}</pre>
			{#if currentSpec}<RuleDiff before={currentSpec} after={draft} caption="Rule in effect → draft" labels={['In effect', 'Draft']} />{/if}
		</details>
	</aside>
{/if}
</div>

<style>
	.crumb {
		margin: 0;
		font-size: var(--text-sm);
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3);
		margin-bottom: var(--space-4);
	}
	.head h1,
	.head p {
		margin: 0;
	}
	.steps {
		display: grid;
		grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
		gap: var(--space-2);
		margin: 0 0 var(--space-5);
		padding: 0;
		list-style: none;
	}
	.step {
		justify-content: flex-start;
		width: 100%;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
		white-space: normal;
		box-shadow: none;
	}
	.step.done {
		color: var(--color-text);
	}
	.step[aria-current='step'] {
		font-weight: 600;
		color: var(--color-text);
		background: var(--color-accent-soft);
		border-color: var(--color-accent);
	}
	.num {
		display: inline-grid;
		place-items: center;
		flex: none;
		width: 1.625rem;
		height: 1.625rem;
		font-size: var(--text-xs);
		font-weight: 700;
		background: var(--color-surface-2);
		border-radius: 50%;
	}
	.done .num {
		color: var(--color-link);
	}
	[aria-current='step'] .num {
		color: var(--color-on-accent);
		background: var(--color-accent);
	}
	.layout {
		display: grid;
		gap: var(--space-5);
		align-items: start;
	}
	.layout.with-side {
		grid-template-columns: minmax(0, 1fr) minmax(18rem, 24rem);
	}
	@media (max-width: 64rem) {
		.layout.with-side {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	.layout > .card {
		min-width: 0;
	}
	.side {
		position: sticky;
		top: var(--space-4);
		display: grid;
		gap: var(--space-3);
		padding: var(--space-4);
		background: var(--color-inset);
	}
	.side p,
	.side h2 {
		margin: 0;
	}
	.side-head {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: var(--space-2);
	}
	.side-head h2 {
		font-size: var(--text-md);
	}
	.sentence {
		padding: var(--space-3);
		font-size: var(--text-sm);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.small {
		font-size: var(--text-xs);
	}
	h2:focus {
		outline: none;
	}
	h3 {
		margin-top: var(--space-5);
	}
	.add {
		margin: 0 0 var(--space-4);
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
	.ack p {
		margin: 0 0 var(--space-2);
	}
	.error {
		color: var(--color-error);
		font-size: var(--text-sm);
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
