<!--
	Dashboard (E21, J21.7): the day at a glance. The owner's cards (GET /settings/dashboard) with
	their resolved value, a neutral delta against the 30-day mean, a sparkline and sources
	(GET /resolved/summary), for today or a past day (?date=). Alerts and connection health stay
	on it. Customize reorders, resizes, hides and adds cards and saves the layout on the server.
	Endpoints that are not available yet (404/503) leave their section empty.
-->
<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { loadProviders } from '#lib/connections/providers.svelte.ts';
	import type { Connection } from '#lib/connections/connections.ts';
	import { addDays, isDate, today } from '#lib/data/format.ts';
	import AddMetric from '#lib/dashboard/AddMetric.svelte';
	import Alerts from '#lib/dashboard/Alerts.svelte';
	import EditTools from '#lib/dashboard/EditTools.svelte';
	import { dashIcons } from '#lib/dashboard/icons.ts';
	import { cardLabel, defaultCards, move, moveTo, patch, type Card, type Catalogue } from '#lib/dashboard/layout.ts';
	import MetricCard from '#lib/dashboard/MetricCard.svelte';
	import Sources from '#lib/dashboard/Sources.svelte';
	import { cardView, loadSummaries, type Summary } from '#lib/dashboard/summary.ts';
	import Button from '#lib/ui/Button.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Skeleton from '#lib/ui/Skeleton.svelte';

	const unavailable = (p: Problem) => p.status === 404 || p.status === 503;
	const week = 7 * 86_400_000;

	// ---- what the owner sees: layout, catalogue, summaries of the chosen day ------------------
	let layout = $state<Card[] | null>(null);
	let layoutMissing = $state(false);
	let layoutProblem = $state<Problem | null>(null);
	let catalogue = $state<Catalogue[]>([]);
	let summaries = $state<Record<string, Summary>>({});
	let summaryMissing = $state(false);
	let summaryProblem = $state<Problem | null>(null);
	let shownDate = $state('');
	let timezone = $state('');

	// ---- alerts and connection health ------------------------------------------------------------
	let connections = $state<Connection[] | null>(null);
	let connectionsProblem = $state<Problem | null>(null);
	let jobs = $state<Schemas['Job'][]>([]);
	let lastBackup = $state<string | null>(null);

	// ---- edit mode -----------------------------------------------------------------------------
	let editing = $state(false);
	let draft = $state<Card[]>([]);
	let saving = $state(false);
	let saveProblem = $state<Problem | null>(null);
	let adding = $state(false);
	let announce = $state('');
	let dragging = $state<string | null>(null);
	let over = $state<string | null>(null);

	const requested = $derived(isDate(page.url.searchParams.get('date')) ? page.url.searchParams.get('date')! : undefined);
	const additive = $derived(new Set(catalogue.filter((m) => m.aggregation === 'additive').map((m) => m.code)));
	const sections = $derived(new Map(catalogue.map((m) => [m.code, m.section])));
	const cards = $derived(editing ? draft : (layout ?? []));
	const visible = $derived(cards.filter((c) => !c.hidden));
	const hidden = $derived(draft.filter((c) => c.hidden));
	const views = $derived(Object.fromEntries(Object.entries(summaries).map(([m, s]) => [m, cardView(m, s, additive.has(m))])));
	const ready = $derived(visible.every((c) => c.metric in summaries));
	// Outside edit mode a card with no data waits (hidden) until a source provides it.
	const shown = $derived(editing ? visible : visible.filter((c) => !ready || views[c.metric]?.hasData));
	const day = $derived(requested ?? (shownDate || today()));
	const isToday = $derived(!requested);
	const dayLabel = $derived.by(() => {
		const [y, m, d] = day.split('-').map(Number);
		return new Date(y, m - 1, d).toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' });
	});

	onMount(() => {
		void loadProviders();
		void api.GET('/api/v1/settings/dashboard').then(({ data, error }) => {
			if (error) {
				if (unavailable(error)) layoutMissing = true;
				else layoutProblem = error;
				layout = [];
			} else layout = data.cards;
		});
		void api.GET('/api/v1/metrics').then(({ data }) => (catalogue = data?.metrics ?? []));
		void api.GET('/api/v1/connections').then(({ data, error }) => {
			connectionsProblem = error ?? null;
			connections = data?.connections ?? [];
		});
		void api.GET('/api/v1/jobs', { params: { query: { status: 'dead', limit: 20 } } }).then(({ data }) => {
			const since = Date.now() - week;
			jobs = (data?.jobs ?? []).filter((j) => Date.parse(j.finished_at ?? j.created_at) >= since);
		});
		void api.GET('/api/v1/system/status').then(({ data }) => (lastBackup = data?.last_backup_at ?? null));
	});

	// Summaries are fetched for the cards on screen, and again for a card shown or pinned later.
	let loadedDay: string | undefined;
	let started = false;
	let asked: string[] = [];
	$effect(() => {
		const metrics = visible.map((c) => c.metric);
		const d = requested;
		untrack(() => void ensure(metrics, d));
	});

	async function ensure(metrics: string[], d: string | undefined) {
		if (!started || d !== loadedDay) {
			started = true;
			loadedDay = d;
			asked = [];
			summaries = {};
			summaryMissing = false;
			summaryProblem = null;
		}
		const need = metrics.filter((m) => !asked.includes(m));
		if (!need.length) return;
		asked.push(...need);
		const r = await loadSummaries(need, d);
		if (d !== loadedDay) return; // another day was chosen meanwhile
		if (r.error) {
			summaryMissing = unavailable(r.error);
			summaryProblem = summaryMissing ? null : r.error;
		}
		summaries = { ...summaries, ...r.summaries };
		if (r.date) shownDate = r.date;
		timezone = r.timezone;
	}

	function chooseDay(d: string | undefined) {
		void goto(d ? `/?date=${d}` : '/', { replace: true, reset: false });
	}

	// ---- editing ---------------------------------------------------------------------------------
	function customize() {
		draft = (layout ?? []).map((c) => ({ ...c }));
		saveProblem = null;
		announce = '';
		editing = true;
	}

	async function save() {
		saving = true;
		saveProblem = null;
		const { data, error } = await api.PUT('/api/v1/settings/dashboard', { body: { version: 1, cards: draft } });
		saving = false;
		if (error) saveProblem = error;
		else {
			layout = data.cards;
			editing = false;
		}
	}

	const change = (metric: string, c: Partial<Card>) => (draft = patch(draft, metric, c));

	function reorder(next: Card[], metric: string) {
		draft = next;
		const at = next.filter((c) => !c.hidden).findIndex((c) => c.metric === metric) + 1;
		announce = `${cardLabel(metric)} is now card ${at} of ${next.filter((c) => !c.hidden).length}.`;
	}

	function dragStart(metric: string, e: DragEvent) {
		dragging = metric;
		if (!e.dataTransfer) return;
		e.dataTransfer.effectAllowed = 'move';
		e.dataTransfer.setData('text/plain', metric);
		const card = (e.currentTarget as HTMLElement).closest('article');
		if (card) e.dataTransfer.setDragImage(card, 24, 24);
	}

	function drop(target: string) {
		if (dragging) reorder(moveTo(draft, dragging, target), dragging);
		dragging = over = null;
	}

	function pin(metric: string) {
		draft = [...draft, { metric, size: 'S', hidden: false }];
		announce = `${cardLabel(metric)} added.`;
	}
</script>

<svelte:head><title>Dashboard · Vitamux</title></svelte:head>

<div class="head">
	<div>
		<h1>Dashboard</h1>
		<p class="muted">{dayLabel}{timezone ? ` · Resolved values for ${timezone}` : ''}</p>
	</div>
	<div class="actions">
		<div class="date" role="group" aria-label="Day shown">
			<button class="btn sm" type="button" aria-label="Previous day" onclick={() => chooseDay(addDays(day, -1))}>
				<Icon d={dashIcons.left} size={16} />
			</button>
			<input type="date" aria-label="Date" value={day} max={today()} onchange={(e) => chooseDay(e.currentTarget.value === today() ? undefined : e.currentTarget.value || undefined)} />
			<button class="btn sm" type="button" aria-label="Next day" disabled={isToday || day >= today()} onclick={() => chooseDay(addDays(day, 1) >= today() ? undefined : addDays(day, 1))}>
				<Icon d={dashIcons.right} size={16} />
			</button>
			{#if !isToday}<Button size="sm" onclick={() => chooseDay(undefined)}>Today</Button>{/if}
		</div>
		{#if !editing}
			<Button onclick={customize} disabled={layout === null || layoutMissing || summaryMissing || !!layoutProblem || !!summaryProblem}>
				<Icon d={dashIcons.edit} size={16} /> Customize
			</Button>
		{/if}
	</div>
</div>

{#if connections === null}
	<p class="muted" role="status">Loading…</p>
{:else}
	<Alerts {connections} {jobs} {lastBackup} />
{/if}

{#if editing}
	<div class="edit-bar" role="region" aria-label="Editing dashboard">
		<Icon d={dashIcons.edit} size={16} />
		<strong>Editing dashboard</strong>
		<span class="muted hint">Drag cards, or use the arrows, to reorder. Pick a size, hide what you do not need. The layout is saved on the server.</span>
		<div class="bar-actions">
			<Button variant="ghost" onclick={() => (draft = defaultCards.map((c) => ({ ...c })))}>Reset to default</Button>
			<Button onclick={() => (adding = true)}><Icon d={dashIcons.plus} size={16} /> Add metric</Button>
			<Button onclick={() => (editing = false)}>Cancel</Button>
			<Button variant="primary" loading={saving} onclick={save}>Save</Button>
		</div>
	</div>
	<ProblemAlert problem={saveProblem} />
	<p class="visually-hidden" role="status">{announce}</p>
{/if}

<section aria-labelledby="metrics">
	<h2 id="metrics">Metrics</h2>
	<ProblemAlert problem={layoutProblem ?? summaryProblem} />
	{#if layout === null}
		<Skeleton variant="block" />
	{:else if layoutMissing || summaryMissing}
		<p class="muted">Resolved values are not available yet.</p>
	{:else if layoutProblem || summaryProblem}
		<!-- shown above -->
	{:else if !editing && !visible.length}
		<EmptyState title="No cards on the dashboard" text="Every card is hidden, or the layout is empty. Customize it to pin metrics.">
			<Button onclick={customize}>Customize</Button>
		</EmptyState>
	{:else if !editing && ready && !shown.length}
		<EmptyState title="No data yet" text="Metrics appear here once a source provides them.">
			<Button href="/connections" variant="primary">Connect a source</Button>
		</EmptyState>
	{:else if !editing && !ready}
		<Skeleton variant="chart" />
	{:else}
		<div class="grid">
			{#each shown as c, i (c.metric)}
				<MetricCard
					code={c.metric}
					section={sections.get(c.metric)}
					label={cardLabel(c.metric)}
					size={c.size}
					view={views[c.metric] ?? null}
					date={shownDate}
					edit={editing}
					over={over === c.metric}
					ondragover={editing ? (e: DragEvent) => { e.preventDefault(); over = c.metric; } : undefined}
					ondrop={editing ? (e: DragEvent) => { e.preventDefault(); drop(c.metric); } : undefined}
				>
					{#snippet tools()}
						{#if editing}
							<EditTools
								label={cardLabel(c.metric)}
								size={c.size}
								first={i === 0}
								last={i === shown.length - 1}
								onsize={(size) => change(c.metric, { size })}
								onmove={(dir) => reorder(move(draft, c.metric, dir), c.metric)}
								onhide={() => change(c.metric, { hidden: true })}
								ondrag={(e) => dragStart(c.metric, e)}
								ondragend={() => (dragging = over = null)}
							/>
						{/if}
					{/snippet}
				</MetricCard>
			{/each}
		</div>
		{#if editing}
			<div class="hidden-cards">
				<h3>Hidden · {hidden.length}</h3>
				{#if hidden.length}
					<ul>
						{#each hidden as c (c.metric)}
							<li>
								{cardLabel(c.metric)}
								<button class="btn sm" type="button" aria-label="Show {cardLabel(c.metric)}" onclick={() => change(c.metric, { hidden: false })}>Show</button>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="muted">Cards you hide wait here.</p>
				{/if}
			</div>
		{/if}
	{/if}
</section>

{#if connections}
	<ProblemAlert problem={connectionsProblem} />
	<Sources {connections} />
{/if}

{#if adding}
	<AddMetric
		{catalogue}
		pinned={draft.map((c) => c.metric)}
		onpin={pin}
		onunpin={(m) => (draft = draft.filter((c) => c.metric !== m))}
		onclose={() => (adding = false)}
	/>
{/if}

<style>
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-end;
		justify-content: space-between;
		gap: var(--space-4);
		margin-bottom: var(--space-4);
	}
	.head h1 {
		margin-bottom: var(--space-1);
	}
	.head p {
		margin: 0;
	}
	.actions,
	.date {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-2);
	}
	.date input {
		min-height: var(--control-h-sm);
		padding: 0 var(--space-2);
		font: inherit;
		font-size: var(--text-sm);
		color: var(--color-text);
		background: var(--color-inset);
		border: 1px solid var(--color-border-strong);
		border-radius: var(--radius-md);
	}
	.date .btn {
		width: var(--control-h-sm);
		padding: 0;
	}
	.edit-bar {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: var(--space-3);
		margin: var(--space-4) 0;
		padding: var(--space-3) var(--space-4);
		font-size: var(--text-sm);
		background: var(--color-accent-soft);
		border: 1px solid color-mix(in srgb, var(--color-accent) 35%, transparent);
		border-radius: var(--radius-lg);
	}
	.hint {
		flex: 1 1 18rem;
	}
	.bar-actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
	}
	section {
		margin: var(--space-5) 0;
	}
	section h2 {
		font-size: var(--text-lg);
	}
	.grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: var(--space-4);
	}
	@media (min-width: 64rem) {
		.grid {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
	}
	.hidden-cards {
		margin-top: var(--space-5);
	}
	.hidden-cards h3 {
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
	.hidden-cards ul {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.hidden-cards li {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
		padding-left: var(--space-3);
		font-size: var(--text-sm);
		border: 1px dashed var(--color-border-strong);
		border-radius: var(--radius-pill);
	}
</style>
