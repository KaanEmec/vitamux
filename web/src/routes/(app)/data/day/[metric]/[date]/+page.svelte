<!--
	All-sources drilldown for one metric and local day: the resolved result and why, an overlay
	of every source's series, which sources the rule used, excluded or ignored, the rule's
	inputs with their records (provenance chain, exclusion) and the day's overrides.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import OverrideDialog, { type OverrideAction } from '#lib/components/OverrideDialog.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog, { type ProvenanceEntity } from '#lib/components/ProvenanceDialog.svelte';
	import ResultStatus from '#lib/components/ResultStatus.svelte';
	import SeriesChart, { type ChartSeries } from '#lib/components/SeriesChart.svelte';
	import StatusIcon, { type Status } from '#lib/components/StatusIcon.svelte';
	import { formatValue, metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';

	type Source = Schemas['SourcesDrilldown']['sources'][number];
	type Query = NonNullable<NonNullable<import('#lib/api/schema.d.ts').paths['/api/v1/measurements']['get']['parameters']>['query']>;

	const metric = $derived(page.params.metric ?? '');
	const date = $derived(page.params.date ?? '');
	const win = $derived({ kind: 'local_day' as const, key: date, local_date: date });

	let result = $state<Schemas['ResolvedValue'] | null>(null);
	let timezone = $state('');
	let resultProblem = $state<Problem | null>(null);
	let sources = $state<Source[]>([]);
	let sourcesProblem = $state<Problem | null>(null);
	let overrides = $state<Schemas['Override'][]>([]);
	let overridesProblem = $state<Problem | null>(null);
	let actionProblem = $state<Problem | null>(null);
	let chart = $state<{ series: ChartSeries[]; unit: string } | null>(null);
	/** First loaded record id per source (same order as `sources`), for the provenance trace. */
	let recordIds = $state<(string | null)[]>([]);
	let chartProblem = $state<Problem | null>(null);
	let loadingChart = $state(true);

	let overrideDialog = $state<{ action: OverrideAction; inputId: string } | null>(null);
	let provenance = $state<{ entity: ProvenanceEntity; id: string } | null>(null);

	const todaysOverrides = $derived(overrides.filter((o) => o.window.local_date === date));
	const groups = $derived((result?.inputs ?? []).flatMap((i) => (i.group ? [i.group] : [])));
	const warningCodes = $derived((result?.warnings ?? []).map((w) => (w.group ? `${w.code} (${w.group})` : w.code)));

	async function loadResult() {
		const [daily, ov] = await Promise.all([
			api.GET('/api/v1/resolved/daily', { params: { query: { start_date: date, end_date: date, metrics: [metric] } } }),
			api.GET('/api/v1/overrides', { params: { query: { metric: [metric], limit: 500 } } })
		]);
		resultProblem = daily.error ?? null;
		if (daily.data) {
			timezone = daily.data.timezone;
			result = daily.data.days.find((d) => d.local_date === date)?.metrics[metric] ?? null;
		}
		overridesProblem = ov.error ?? null;
		if (ov.data) overrides = ov.data.overrides;
	}

	async function loadSources() {
		const res = await api.GET('/api/v1/resolved/{metric}/{window_key}/sources', {
			params: { path: { metric, window_key: date } }
		});
		sourcesProblem = res.error ?? null;
		if (res.data) sources = res.data.sources;
	}

	/** The measurements query of a source: its records link when it has one, else a provider filter. */
	function recordsQuery(s: Source): Query {
		const q: Query = { limit: 10000 };
		if (s.records) {
			const p = new URL(s.records.href, location.origin).searchParams;
			q.metric = p.getAll('metric');
			for (const k of ['provider', 'connection', 'device', 'origin'] as const) {
				if (p.has(k)) q[k] = p.getAll(k);
			}
			for (const k of ['start', 'end'] as const) {
				const v = p.get(k);
				if (v) q[k] = v;
			}
			if (!q.start && !q.end) {
				q.start_date = date;
				q.end_date = date;
			}
			return q;
		}
		q.metric = [metric];
		q.provider = [s.provider];
		if (s.origin?.key) q.origin = [s.origin.key];
		q.start_date = date;
		q.end_date = date;
		return q;
	}

	function label(s: Source): string {
		const parts = [s.group ?? s.provider];
		if (s.group && s.group !== s.provider) parts.push(s.provider);
		const detail = s.origin?.name ?? s.origin?.key ?? s.device?.type;
		if (detail) parts.push(detail);
		return parts.join(' · ');
	}

	async function loadChart() {
		loadingChart = true;
		const series: ChartSeries[] = [];
		const ids: (string | null)[] = [];
		let unit = '';
		const loaded = await Promise.all(
			sources.map(async (s) => {
				const q = recordsQuery(s);
				return readAll(async (cursor) => {
					const res = await api.GET('/api/v1/measurements', { params: { query: { ...q, cursor } } });
					if (res.error) return { items: [], problem: res.error };
					return { items: res.data.measurements, next: res.data.has_more ? res.data.next_cursor : undefined };
				});
			})
		);
		chartProblem = null;
		loaded.forEach((r, i) => {
			chartProblem ??= r.problem ?? null;
			ids[i] = r.items[0]?.id ?? null;
			if (!r.items.length) return;
			unit ||= r.items[0].unit;
			const xs: number[] = [];
			const ys: number[] = [];
			for (const m of r.items) {
				const x = Date.parse(m.start_at) / 1000;
				if (xs.length && x === xs[xs.length - 1]) ys[ys.length - 1] = m.value;
				else {
					xs.push(x);
					ys.push(m.value);
				}
			}
			series.push({ label: label(sources[i]), xs, ys });
		});
		recordIds = ids;
		chart = { series, unit };
		loadingChart = false;
	}

	onMount(async () => {
		await Promise.all([loadResult(), loadSources()]);
		await loadChart();
	});

	function saved() {
		overrideDialog = null;
		void loadResult().then(loadSources);
	}

	async function revoke(id: string) {
		actionProblem = null;
		const res = await api.POST('/api/v1/overrides/{id}/revoke', { params: { path: { id } } });
		if (res.error) actionProblem = res.error;
		else await loadResult().then(loadSources);
	}

	function sourceIcon(s: Source): Status {
		return s.rule_status === 'used' ? 'ok' : s.rule_status === 'excluded' ? 'error' : 'off';
	}
	const sourceStatusLabel: Record<string, string> = { used: 'Used', excluded: 'Excluded', not_in_rule: 'Not in rule' };

	function inputIcon(status: string): Status {
		return status === 'used' ? 'ok' : status === 'no_data' ? 'off' : 'warn';
	}

	function describe(o: Schemas['Override']): string {
		if (o.action === 'exclude_input') return `Exclude input ${o.input_id}`;
		if (o.action === 'force_source') return `Force source ${o.group}`;
		return `Set value ${o.value} ${o.unit ?? ''}`.trim();
	}

	const baseValues = (v: Record<string, number> | undefined) =>
		Object.entries(v ?? {}).map(([k, x]) => `${k.replaceAll('_', ' ')} ${formatValue(x)}`);

	const when = (iso: string) => new Date(iso).toLocaleString();
</script>

<svelte:head><title>{metricLabel(metric)} {date} · Vitamux</title></svelte:head>

<p><a href="/data?metric={encodeURIComponent(metric)}&end={date}">← Daily values</a></p>
<h2>{metricLabel(metric)}, {date}</h2>

<section class="card" aria-labelledby="result-h" aria-live="polite">
	<h3 id="result-h">Resolved value</h3>
	<ProblemAlert problem={resultProblem} />
	{#if result}
		<p class="headline">
			<ResultStatus status={result.status} />
			<strong class="value">{result.status === 'no_data' ? '–' : formatValue(result.value, result.unit)}</strong>
			{#if result.partial}<span class="muted">(partial)</span>{/if}
		</p>
		<p class="explanation">{result.explanation}</p>
		{#if warningCodes.length}<p class="warn">Warnings: {warningCodes.join(', ')}</p>{/if}
		<p class="muted meta">
			{#if result.rule}Rule {result.rule.ref} v{result.rule.version}{#if result.rule.strategy}, {result.rule.strategy}{/if}.{/if}
			{#if result.computed_at}Computed {when(result.computed_at)}.{/if}
			{timezone ? `Timezone ${timezone}.` : ''}
		</p>
	{:else if !resultProblem}
		<p class="muted">No resolved value for this day.</p>
	{/if}
	<div class="actions">
		<button class="btn" type="button" onclick={() => (overrideDialog = { action: 'exclude_input', inputId: '' })}>Exclude an input…</button>
		<button class="btn" type="button" onclick={() => (overrideDialog = { action: 'force_source', inputId: '' })} disabled={!groups.length}>Force a source…</button>
		<button class="btn" type="button" onclick={() => (overrideDialog = { action: 'set_value', inputId: '' })}>Set a value…</button>
	</div>
</section>

<section class="card" aria-labelledby="chart-h">
	<h3 id="chart-h">Every source over the day</h3>
	<ProblemAlert problem={chartProblem} />
	{#if loadingChart}
		<p class="muted">Loading series…</p>
	{:else if chart && chart.series.length}
		<SeriesChart
			series={chart.series}
			unit={chart.unit}
			{timezone}
			summary="{metricLabel(metric)} on {date}: {chart.series.map((s) => s.label).join(', ')}"
		/>
	{:else}
		<p class="muted">No measurements from any source on this day.</p>
	{/if}
</section>

<section class="card" aria-labelledby="sources-h">
	<h3 id="sources-h">Sources</h3>
	<p class="muted">Every source seen for this window, including excluded ones and those outside the rule.</p>
	<ProblemAlert problem={sourcesProblem} />
	{#if sources.length}
		<table>
			<thead>
				<tr><th scope="col">Source</th><th scope="col">Status</th><th scope="col">Reason</th><th scope="col">Values</th><th scope="col">Provenance</th></tr>
			</thead>
			<tbody>
				{#each sources as s, i (i)}
					{@const traceId = recordIds[i]}
					<tr>
						<th scope="row">{label(s)}</th>
						<td><StatusIcon status={sourceIcon(s)} /> {sourceStatusLabel[s.rule_status] ?? s.rule_status}</td>
						<td>{s.reason ?? '–'}</td>
						<td>{#each baseValues(s.values) as v (v)}<div>{v}</div>{:else}–{/each}</td>
						<td>
							{#if s.provenance}
								<div class="muted small">
									{s.provenance.normalizer ?? ''}
									{#if s.provenance.fetched_at}· fetched {when(s.provenance.fetched_at)}{/if}
								</div>
							{/if}
							{#if traceId}
								<button class="btn link" type="button" onclick={() => (provenance = { entity: 'measurement', id: traceId })}>
									Trace a record<span class="visually-hidden"> of {label(s)}</span>
								</button>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	{:else if !sourcesProblem}
		<p class="muted">No sources.</p>
	{/if}
</section>

{#if result?.inputs?.length}
	<section class="card" aria-labelledby="inputs-h">
		<h3 id="inputs-h">Rule inputs</h3>
		<table>
			<thead>
				<tr><th scope="col">Group</th><th scope="col">Status</th><th scope="col">Value</th><th scope="col">Basis</th><th scope="col">Coverage</th><th scope="col">Records</th></tr>
			</thead>
			<tbody>
				{#each result.inputs as inp (inp.group)}
					<tr>
						<th scope="row">{inp.group}</th>
						<td>
							<StatusIcon status={inputIcon(inp.status)} />
							{inp.status.replaceAll('_', ' ')}{#if inp.selected}<strong> · selected</strong>{/if}
							{#if inp.reason}<div class="muted small">{inp.reason}</div>{/if}
						</td>
						<td>{inp.value == null ? '–' : formatValue(inp.value, result.unit)}</td>
						<td>{inp.basis?.replaceAll('_', ' ') ?? '–'}</td>
						<td>{inp.coverage == null ? '–' : `${Math.round(inp.coverage * 100)}%`}</td>
						<td>
							{#each inp.record_refs ?? [] as ref (ref)}
								<div class="record">
									<code>{ref}</code>
									<button class="btn link" type="button" onclick={() => (provenance = { entity: 'measurement', id: ref })}>
										Provenance<span class="visually-hidden"> of record {ref}</span>
									</button>
									<button class="btn link" type="button" onclick={() => (overrideDialog = { action: 'exclude_input', inputId: ref })}>
										Exclude<span class="visually-hidden"> record {ref}</span>
									</button>
								</div>
							{:else}–{/each}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</section>
{/if}

<section class="card" aria-labelledby="ov-h">
	<h3 id="ov-h">Overrides for this day</h3>
	<ProblemAlert problem={overridesProblem} />
	<ProblemAlert problem={actionProblem} />
	{#if todaysOverrides.length}
		<ul class="overrides">
			{#each todaysOverrides as o (o.id)}
				<li class:revoked={!o.active}>
					<span>
						<strong>{describe(o)}</strong>
						{#if o.note}<span class="muted"> · {o.note}</span>{/if}
						<span class="muted small"> · {o.active ? 'active' : `revoked ${o.revoked_at ? when(o.revoked_at) : ''}`}</span>
					</span>
					{#if o.active}
						<button class="btn" type="button" onclick={() => revoke(o.id)}>
							Revoke<span class="visually-hidden"> {describe(o)}</span>
						</button>
					{/if}
				</li>
			{/each}
		</ul>
	{:else if !overridesProblem}
		<p class="muted">No overrides on this day.</p>
	{/if}
</section>

{#if overrideDialog}
	<OverrideDialog
		{metric}
		window={win}
		action={overrideDialog.action}
		inputId={overrideDialog.inputId}
		{groups}
		unit={result?.unit ?? ''}
		onsaved={saved}
		onclose={() => (overrideDialog = null)}
	/>
{/if}
{#if provenance}
	<ProvenanceDialog entity={provenance.entity} id={provenance.id} onclose={() => (provenance = null)} />
{/if}

<style>
	section {
		margin-bottom: var(--space-5);
	}
	.headline {
		display: flex;
		gap: var(--space-3);
		align-items: baseline;
		font-size: var(--text-lg);
	}
	.value {
		font-size: var(--text-xl);
		font-variant-numeric: tabular-nums;
	}
	.explanation {
		margin: 0 0 var(--space-2);
	}
	.warn {
		color: var(--color-warn);
	}
	.meta,
	.small {
		font-size: var(--text-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin-top: var(--space-3);
	}
	table {
		width: 100%;
		border-collapse: collapse;
	}
	th,
	td {
		padding: var(--space-2) var(--space-3);
		text-align: left;
		vertical-align: top;
		border-bottom: 1px solid var(--color-border);
	}
	thead th {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.record {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: baseline;
	}
	.overrides {
		display: grid;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.overrides li {
		display: flex;
		gap: var(--space-3);
		align-items: center;
		justify-content: space-between;
	}
	.revoked {
		opacity: 0.65;
	}
</style>
