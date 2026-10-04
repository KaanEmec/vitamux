<!--
	Body composition (J21.9): weight per source, the latest weigh-in of each day as fat-free mass
	plus fat mass, and every component of every weigh-in in a table (group body_composition).
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import RangePicker, { type RangeKey } from '#lib/charts/RangePicker.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import ProvenanceDialog from '#lib/components/ProvenanceDialog.svelte';
	import { clock, formatValue, metricLabel } from '#lib/data/format.ts';
	import { readAll } from '#lib/data/paging.ts';
	import Chip from '#lib/ui/Chip.svelte';
	import EmptyState from '#lib/ui/EmptyState.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Skeleton from '#lib/ui/Skeleton.svelte';
	import { dayMs, mean, providerName, recordLabel } from '#lib/views/format.ts';
	import { rangeDates } from '#lib/views/range.ts';
	import Stats from '#lib/views/Stats.svelte';
	import TableCard from '#lib/views/TableCard.svelte';
	import ViewHead from '#lib/views/ViewHead.svelte';

	type Group = Schemas['Group'];

	const pageSize = 50;
	// Column order of the table; other components follow alphabetically.
	const names: Record<string, string> = { fat_free_mass: 'Fat-free mass' };
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

	const weights = $derived(groups.filter((g) => value(g, 'weight') != null));
	const lines = $derived(
		Object.entries(Object.groupBy(weights, (g) => g.source.provider)).map(([provider, list = []]) => ({
			label: providerName(provider),
			source: provider,
			xs: list.map((g) => Date.parse(g.measured_at)),
			ys: list.map((g) => value(g, 'weight') ?? 0)
		}))
	);

	// The latest weigh-in of each day that has both parts of the weight (groups are oldest first).
	const days = $derived(
		Object.values(
			Object.fromEntries(
				groups.filter((g) => value(g, 'fat_mass') != null && value(g, 'fat_free_mass') != null).map((g) => [g.local_date, g])
			)
		)
	);

	const last = $derived(weights.at(-1));
	const stats = $derived.by(() => {
		const avg = mean(weights.map((g) => value(g, 'weight') ?? 0));
		const fat = groups.findLast((g) => value(g, 'fat_mass') != null);
		return [
			...(last ? [{ k: `Latest weight · ${last.local_date}`, v: formatValue(value(last, 'weight')), u: part(last, 'weight')?.unit }] : []),
			{ k: 'Mean weight in range', v: avg == null ? '–' : formatValue(avg), u: last && part(last, 'weight')?.unit },
			{ k: 'Weigh-ins in range', v: String(groups.length) },
			...(fat ? [{ k: `Latest fat mass · ${fat.local_date}`, v: formatValue(value(fat, 'fat_mass')), u: part(fat, 'fat_mass')?.unit }] : [])
		];
	});

	const columns = $derived.by(() => {
		const units = Object.fromEntries(groups.flatMap((g) => g.components.map((c) => [c.metric, c.unit])));
		const rank = (code: string) => (order.includes(code) ? order.indexOf(code) : order.length);
		return Object.entries(units)
			.sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
			.map(([code, unit]) => ({ code, label: `${names[code] ?? metricLabel(code)} (${unit})` }));
	});
	const newest = $derived([...groups].reverse());
</script>

<svelte:head><title>Body composition · Vitamux</title></svelte:head>

<ViewHead title="Body composition" text="Weight and the parts a scale reports with it, one weigh-in at a time, from every source.">
	<RangePicker bind:value={rangeKey} options={['1M', '3M', '1Y', 'All']} />
</ViewHead>

<ProblemAlert {problem} />

{#if loading}
	<Skeleton variant="chart" label="Loading weigh-ins" />
{:else if !groups.length}
	{#if !problem}<EmptyState icon={icons.explore} title="No weigh-ins in this range" text="Weigh-ins from a connected scale or a manual entry appear here." />{/if}
{:else}
	<Stats items={stats} />

	{#if lines.length}
		<section class="card" aria-labelledby="weight-h">
			<h2 id="weight-h">Weight</h2>
			<p class="muted note">One line per source, a point per weigh-in.</p>
			{#await import('#lib/charts/TimeSeries.svelte') then { default: TimeSeries }}
				<TimeSeries series={lines} label="Weight per weigh-in" unit="kg" />
			{/await}
		</section>
	{/if}

	{#if days.length}
		<section class="card" aria-labelledby="stack-h">
			<h2 id="stack-h">Fat-free mass and fat mass</h2>
			<p class="muted note">The latest weigh-in of each day that reports both; together they make up the weight.</p>
			{#await import('#lib/charts/Bars.svelte') then { default: Bars }}
				<Bars
					xs={days.map((g) => dayMs(g.local_date))}
					stacks={[
						{ label: 'Fat-free mass', color: 'accent', ys: days.map((g) => value(g, 'fat_free_mass')) },
						{ label: 'Fat mass', color: 'info', ys: days.map((g) => value(g, 'fat_mass')) }
					]}
					label="Fat-free mass and fat mass per day, stacked"
					unit="kg"
					timezone="UTC"
				/>
			{/await}
		</section>
	{/if}

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
{/if}

{#if provenanceId}
	<ProvenanceDialog entity="group" id={provenanceId} onclose={() => (provenanceId = null)} />
{/if}

<style>
	.card {
		margin-bottom: var(--space-4);
	}
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
</style>
