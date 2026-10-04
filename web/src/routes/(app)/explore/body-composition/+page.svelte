<!--
	Body composition (J23.8): weight as one dot per weigh-in with its 7-day moving average, the
	latest value and the change in the range, a tile with a sparkline and a neutral change for each
	other component, and every component of every weigh-in in a table (group body_composition).
	Values are shown as measured: no classes, no thresholds.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import Sparkline from '#lib/charts/Sparkline.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { clock, formatValue, metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import { metricLook } from '#lib/ui/metric.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { mean, recordLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import TableCard from '#lib/views/TableCard.svelte';
	import ViewHead from '#lib/views/ViewHead.svelte';

	type Group = Schemas['Group'];

	const look = metricLook('body_composition');
	const pageSize = 50;
	const week = 7 * 86_400_000;
	const ranges: RangeKey[] = ['1M', '3M', '1Y', 'All'];
	const labels = { '1M': '30D', '3M': '90D' };
	// Column order of the table and the tiles; other components follow alphabetically.
	const names: Record<string, string> = { fat_free_mass: 'Fat-free mass', body_fat_ratio: 'Body fat' };
	const order = ['weight', 'body_fat_ratio', 'fat_mass', 'fat_free_mass', 'muscle_mass', 'bone_mass', 'hydration'];

	let rangeKey = $state<RangeKey>('3M');
	let groups = $state<Group[]>([]);
	let problem = $state<Problem | null>(null);
	let loading = $state(true);
	let shown = $state(pageSize);
	let provenanceId = $state<string | null>(null);

	$effect(() => {
		const r = rangeDates(rangeKey);
		let stale = false;
		loading = true;
		void readAll<Group>(async (cursor) => {
			const res = await api.GET('/api/v1/groups', { params: { query: { kind: 'body_composition', start_date: r.start, end_date: r.end, limit: 500, cursor } } });
			if (res.error) return { items: [], problem: res.error };
			return { items: res.data.groups, next: res.data.has_more ? res.data.next_cursor : undefined };
		}).then((res) => {
			if (stale) return;
			loading = false;
			groups = res.items;
			problem = res.problem ?? null;
			shown = pageSize;
		});
		return () => (stale = true);
	});

	const part = (g: Group, code: string) => g.components.find((c) => c.metric === code);
	const value = (g: Group, code: string) => part(g, code)?.value ?? null;
	const signed = (v: number) => `${v < 0 ? '−' : v > 0 ? '+' : ''}${formatValue(Math.abs(v))}`;
	const rank = (code: string) => (order.includes(code) ? order.indexOf(code) : order.length);

	// Weight: a dot per weigh-in and the mean of the weigh-ins in the 7 days up to each one.
	const weights = $derived(groups.filter((g) => value(g, 'weight') != null));
	const wx = $derived(weights.map((g) => Date.parse(g.measured_at)));
	const wy = $derived(weights.map((g) => value(g, 'weight') ?? 0));
	const average = $derived(wx.map((t, i) => mean(wy.filter((_, j) => j <= i && wx[j] > t - week))));
	const last = $derived(weights.at(-1));
	const unit = $derived((last && part(last, 'weight')?.unit) ?? 'kg');
	const change = $derived(weights.length > 1 ? wy[wy.length - 1] - wy[0] : null);

	// One tile per other component: its latest value, a sparkline and the change since the first weigh-in that has it.
	const tiles = $derived.by(() => {
		const codes = [...new Set(groups.flatMap((g) => g.components.map((c) => c.metric)))].filter((c) => c !== 'weight');
		return codes
			.sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
			.map((code) => {
				const gs = groups.filter((g) => value(g, code) != null);
				const [first, latest] = [gs[0], gs[gs.length - 1]];
				const [v0, v1] = [value(first, code) ?? 0, value(latest, code) ?? 0];
				const delta = gs.length < 2 ? 'Single weigh-in' : Math.abs(v1 - v0) < 0.05 ? `No change since ${first.local_date}` : `${signed(v1 - v0)} ${part(latest, code)?.unit ?? ''} since ${first.local_date}`;
				return { code, label: names[code] ?? metricLabel(code), unit: part(latest, code)?.unit, value: v1, ys: gs.map((g) => value(g, code)), delta, source: latest.source };
			});
	});

	const columns = $derived.by(() => {
		const units = Object.fromEntries(groups.flatMap((g) => g.components.map((c) => [c.metric, c.unit])));
		return Object.entries(units)
			.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
			.map(([code, u]) => ({ code, label: `${names[code] ?? metricLabel(code)} (${u})` }));
	});
	const newest = $derived(groups.toReversed());
</script>

<svelte:head><title>Body composition · Vitamux</title></svelte:head>

<ViewHead title="Body composition" text="Weight and the parts a scale reports with it, shown as measured. Nothing is graded or flagged." tile={{ code: 'body_composition' }}>
	<RangePicker bind:value={rangeKey} options={ranges} {labels} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading weigh-ins" />
{:else if !groups.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No weigh-ins in this range" text="Weigh-ins from a connected scale or a manual entry appear here." />{/if}
{:else}
	<div class="body" style:--metric={look.color}>
		<div class="grid">
			{#if weights.length}
				<section class="card" aria-labelledby="weight-h">
					<div class="head">
						<div>
							<h2 id="weight-h">Weight</h2>
							<p class="muted note">One dot per weigh-in, with the mean of the weigh-ins in the 7 days up to each as a line.</p>
						</div>
						<dl>
							<div>
								<dt>Latest · {last?.local_date}</dt>
								<dd>{formatValue(wy[wy.length - 1])} <span class="unit">{unit}</span></dd>
							</div>
							{#if change != null}
								<div>
									<dt>Change in range</dt>
									<dd>{signed(change)} <span class="unit">{unit}</span></dd>
								</div>
							{/if}
						</dl>
					</div>
					{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
						<TimeSeries
							series={[
								{ label: 'Weigh-in', xs: wx, ys: wy, style: 'dots', providers: weights.map((g) => [g.source.provider]) },
								{ label: '7-day average', xs: wx, ys: average, style: 'trend' }
							]}
							label="Weight per weigh-in"
							{unit}
							height={260}
						/>
					{/await}
				</section>
			{/if}

			{#if tiles.length}
				<section class="card" aria-labelledby="comp-h">
					<h2 id="comp-h">Body composition</h2>
					<p class="muted note">The latest value of each part, and its change since the first weigh-in in the range.</p>
					<ul class="tiles">
						{#each tiles as t (t.code)}
							<li class="tile">
								<div class="top">
									<span class="muted">{t.label}</span>
									<span class="spark"><Sparkline ys={t.ys} /></span>
								</div>
								<span class="value num">{formatValue(t.value)} {#if t.unit}<span class="unit">{t.unit}</span>{/if}</span>
								<span class="delta num muted">{t.delta}</span>
								<Chip source={t.source.provider}>{recordLabel(t.source)}</Chip>
							</li>
						{/each}
					</ul>
				</section>
			{/if}
		</div>

		<TableCard title="Weigh-ins" remaining={newest.length - shown} onmore={() => (shown += pageSize)}>
			<thead>
				<tr>
					<th scope="col">Measured</th>
					{#each columns as c (c.code)}<th scope="col" class="num">{c.label}</th>{/each}
					<th scope="col">Source</th><th scope="col"><span class="visually-hidden">Provenance</span></th>
				</tr>
			</thead>
			<tbody>
				{#each newest.slice(0, shown) as g (g.id)}
					<tr>
						<th scope="row">{g.local_date} {clock(g.measured_at, g.tz_offset_min)}</th>
						{#each columns as c (c.code)}<td class="num">{formatValue(value(g, c.code))}</td>{/each}
						<td><Chip source={g.source.provider}>{recordLabel(g.source)}</Chip></td>
						<td>
							<button class="btn link" type="button" onclick={() => (provenanceId = g.id)}>
								Provenance<span class="visually-hidden"> of the weigh-in on {g.local_date} {clock(g.measured_at, g.tz_offset_min)}</span>
							</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</TableCard>
	</div>
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="group" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.body {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	.grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: var(--space-4);
		min-width: 0;
	}
	.grid > * {
		min-width: 0;
	}
	@media (min-width: 64rem) {
		.grid {
			grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
			align-items: start;
		}
	}
	.head {
		display: flex;
		flex-wrap: wrap;
		align-items: flex-start;
		justify-content: space-between;
		gap: var(--space-3) var(--space-5);
		margin-bottom: var(--space-4);
	}
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		max-width: 34rem;
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
	.head .note {
		margin: 0;
	}
	dl {
		display: flex;
		gap: var(--space-5);
		margin: 0;
	}
	dt {
		font-size: var(--text-2xs);
		text-transform: uppercase;
		letter-spacing: var(--tracking-label);
		color: var(--color-text-faint);
	}
	dd {
		margin: var(--space-1) 0 0;
		font-size: var(--text-xl);
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.unit {
		font-size: var(--text-xs);
		font-weight: 400;
		color: var(--color-text-muted);
	}
	.tiles {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tile {
		display: grid;
		justify-items: start;
		gap: var(--space-2);
		padding: var(--space-3);
		background: var(--color-inset);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-md);
	}
	.top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: var(--space-2);
		width: 100%;
		font-size: var(--text-xs);
	}
	.spark {
		width: 4rem;
	}
	.spark :global(.spark) {
		height: 1.25rem;
	}
	.value {
		font-size: var(--text-xl);
		font-weight: 600;
	}
	.delta {
		font-size: var(--text-2xs);
	}
</style>
