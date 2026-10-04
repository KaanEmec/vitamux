<!--
	Today: alerts (connections needing attention, permanently failed jobs, a stale backup),
	the key resolved metrics with the sources they came from, and one health card per
	connection. Endpoints that are not available yet (404/503) leave their section empty.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import HealthBadge from '#lib/components/HealthBadge.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ResultStatus from '#lib/components/ResultStatus.svelte';
	import StatusIcon, { type Status } from '#lib/components/StatusIcon.svelte';
	import UnofficialBadge from '#lib/components/UnofficialBadge.svelte';
	import { addDays, formatValue, metricLabel, today } from '#lib/data/format.ts';
	import { loadProviders } from '#lib/connections/providers.svelte.ts';
	import { ago, alerting, providerLabel, type Connection } from '#lib/connections/connections.ts';

	type Resolved = Schemas['ResolvedValue'];
	interface Tile {
		metric: string;
		date: string;
		value?: Resolved;
	}
	interface Alert {
		status: Status;
		text: string;
		href?: string;
		action?: string;
	}

	// The owner's key metrics, in tile order (codes from docs/metrics.md).
	const keyMetrics = ['resting_heart_rate', 'hrv_rmssd_nightly', 'sleep_total', 'steps', 'weight', 'blood_pressure'];
	const week = 7 * 86_400_000;
	const backupMaxAge = 8 * 86_400_000;
	const unavailable = (p: Problem) => p.status === 404 || p.status === 503;

	const date = today();
	const yesterday = addDays(date, -1);

	let tiles = $state<Tile[] | null>(null);
	let resolvedProblem = $state<Problem | null>(null);
	let resolvedMissing = $state(false);
	let connections = $state<Connection[] | null>(null);
	let connectionsProblem = $state<Problem | null>(null);
	let jobs = $state<Schemas['Job'][]>([]);
	let lastBackup = $state<string | null>(null);

	onMount(() => {
		void loadProviders();
		void loadResolved();
		void api.GET('/api/v1/connections').then(({ data, error }) => {
			connectionsProblem = error ?? null;
			connections = data?.connections ?? [];
		});
		void api.GET('/api/v1/jobs', { params: { query: { status: 'dead', limit: 20 } } }).then(({ data }) => {
			const since = Date.now() - week;
			jobs = (data?.jobs ?? []).filter((j) => Date.parse(j.finished_at ?? j.created_at) >= since);
		});
		void api.GET('/api/v1/system/status').then(({ data }) => {
			if (data) lastBackup = backupTime(data);
		});
	});

	async function loadResolved() {
		const { data, error } = await api.GET('/api/v1/resolved/daily', {
			params: { query: { start_date: yesterday, end_date: date, metrics: keyMetrics } },
			querySerializer: { array: { style: 'form', explode: false } } // metrics=a,b (spec: explode false)
		});
		if (error) {
			if (unavailable(error)) resolvedMissing = true;
			else resolvedProblem = error;
			tiles = [];
			return;
		}
		const on = (d: string) => data.days.find((x) => x.local_date === d)?.metrics ?? {};
		const [t, y] = [on(date), on(yesterday)];
		tiles = keyMetrics.map((metric) => {
			const v = t[metric];
			if (v && v.status !== 'no_data') return { metric, date, value: v };
			const w = y[metric];
			if (w && w.status !== 'no_data') return { metric, date: yesterday, value: w };
			return { metric, date, value: v };
		});
	}

	// GET /system/status is an open object (J10.5); read the last backup time defensively.
	function backupTime(s: Record<string, unknown>): string | null {
		const b = s.last_backup ?? s.last_backup_at ?? (s.backup as Record<string, unknown> | undefined)?.last_success_at;
		if (typeof b === 'string') return b;
		if (b && typeof b === 'object') {
			const o = b as Record<string, unknown>;
			const t = o.finished_at ?? o.at ?? o.created_at;
			return typeof t === 'string' ? t : null;
		}
		return null;
	}

	/** Source chips: the selected inputs' groups, with their provider when it adds information. */
	function chips(v: Resolved | undefined): string[] {
		return (v?.inputs ?? []).flatMap((i) => {
			if (!i.selected || !i.group) return [];
			const p = i.sources?.[0]?.provider;
			return [p && p !== i.group ? `${i.group} · ${providerLabel(p)}` : i.group];
		});
	}

	function connectionAlert(c: Connection): Alert {
		const name = providerLabel(c.provider);
		const href = `/connections/${c.id}`;
		switch (c.health) {
			case 'needs_reauth':
				return { status: 'error', text: `${name} needs reauthorization.`, href, action: `Reauthorize ${name}` };
			case 'failing':
				return {
					status: 'error',
					text: `${name} is failing (${c.consecutive_failures} failed runs${c.last_error_class ? `, ${c.last_error_class}` : ''}).`,
					href: `${href}?tab=history`,
					action: 'See history'
				};
			case 'stale':
				return { status: 'warn', text: `${name} has not synced successfully since ${ago(c.last_success_at)}.`, href, action: 'Open' };
			default:
				return {
					status: 'warn',
					text: `${name} is degraded${c.health_reason ? `: ${c.health_reason}` : '.'}`,
					href: `${href}?tab=streams`,
					action: 'See streams'
				};
		}
	}

	const alerts = $derived.by(() => {
		const out: Alert[] = (connections ?? []).filter((c) => alerting.includes(c.health)).map(connectionAlert);
		for (const j of jobs.slice(0, 5)) {
			out.push({
				status: 'error',
				text: `Job ${j.kind} failed permanently after ${j.attempts} attempts, ${ago(j.finished_at ?? j.created_at)}.`,
				href: j.connection_id ? `/connections/${j.connection_id}?tab=history` : undefined,
				action: j.connection_id ? 'See history' : undefined
			});
		}
		if (jobs.length > 5) out.push({ status: 'error', text: `${jobs.length - 5} more jobs failed permanently this week.` });
		if (lastBackup && Date.now() - Date.parse(lastBackup) > backupMaxAge) {
			out.push({ status: 'warn', text: `The last backup is from ${ago(lastBackup)}.`, href: '/settings', action: 'Backups' });
		}
		return out;
	});
</script>

<svelte:head><title>Today · Vitamux</title></svelte:head>

<h1>Today</h1>

<section aria-labelledby="alerts">
	<h2 id="alerts">Alerts</h2>
	{#if connections === null}
		<p class="muted" role="status">Loading…</p>
	{:else if alerts.length}
		<ul class="alerts">
			{#each alerts as a, i (i)}
				<li class={a.status}>
					<StatusIcon status={a.status} />
					<span>{a.text}</span>
					{#if a.href && a.action}<a href={a.href}>{a.action}</a>{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="quiet"><StatusIcon status="ok" /> Nothing needs your attention.</p>
	{/if}
</section>

<section aria-labelledby="metrics">
	<h2 id="metrics">Key metrics</h2>
	<ProblemAlert problem={resolvedProblem} />
	{#if tiles === null}
		<p class="muted" role="status">Loading…</p>
	{:else if resolvedMissing}
		<p class="quiet"><StatusIcon status="info" /> Resolved values are not available yet.</p>
	{:else if tiles.length}
		<ul class="tiles">
			{#each tiles as t (t.metric)}
				<li class="card tile">
					<h3>{metricLabel(t.metric)}</h3>
					<p class="value">{formatValue(t.value?.value, t.value?.unit)}</p>
					<p class="meta">
						<ResultStatus status={t.value?.status ?? 'no_data'} />
						<span class="muted">{t.date === date ? 'Today' : 'Yesterday'}</span>
					</p>
					{#if chips(t.value).length}
						<ul class="chips" aria-label="Sources">
							{#each chips(t.value) as c (c)}<li>{c}</li>{/each}
						</ul>
					{/if}
					{#if t.value && t.value.status !== 'no_data'}
						<a href="/data/day/{t.metric}/{t.date}">All sources<span class="visually-hidden"> for {metricLabel(t.metric)}</span></a>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</section>

<section aria-labelledby="health">
	<h2 id="health">Connections</h2>
	<ProblemAlert problem={connectionsProblem} />
	{#if connections?.length}
		<ul class="tiles">
			{#each connections as c (c.id)}
				<li class="card tile">
					<h3>
						<a href="/connections/{c.id}">{providerLabel(c.provider)}</a>
						{#if c.official === false}<UnofficialBadge />{/if}
					</h3>
					<p><HealthBadge health={c.health} /></p>
					<p class="muted">Last success {ago(c.last_success_at)}</p>
					{#if c.health_reason}<p class="muted reason">{c.health_reason}</p>{/if}
				</li>
			{/each}
		</ul>
	{:else if connections && !connectionsProblem}
		<p class="muted">No connections yet. <a href="/connections">Connect a source</a>.</p>
	{/if}
</section>

<style>
	section {
		margin-bottom: var(--space-6);
	}
	.alerts {
		display: grid;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.alerts li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-3);
		background: var(--color-surface);
		border: 1px solid var(--color-border);
		border-left: 4px solid var(--color-warn);
		border-radius: var(--radius-sm);
	}
	.alerts li.error {
		border-left-color: var(--color-error);
	}
	.quiet {
		display: flex;
		gap: var(--space-2);
		align-items: center;
	}
	.tiles {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(14rem, 1fr));
		gap: var(--space-4);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tile {
		display: grid;
		align-content: start;
		gap: var(--space-2);
		padding: var(--space-4);
	}
	.tile h3 {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		margin: 0;
		font-size: var(--text-md);
	}
	.tile p {
		margin: 0;
	}
	.value {
		font-size: var(--text-2xl);
		font-weight: 600;
	}
	.meta {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		font-size: var(--text-sm);
	}
	.reason {
		font-size: var(--text-sm);
	}
	.chips {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.chips li {
		padding: 0 var(--space-2);
		font-size: var(--text-xs);
		background: var(--color-surface-2);
		border: 1px solid var(--color-border);
		border-radius: 999px;
	}
</style>
