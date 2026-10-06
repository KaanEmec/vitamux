<!--
	The origins whose records of a metric in [start, end) are held raw because a device's Apple Health
	source filter ignores them (GET /sources/series with include_ignored, J22.25). They have no values,
	so only the origin, the record count and the time span are listed. Mount it only while the
	"Ignored sources" toggle is on; it never feeds the chart.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';

	let { metric, start, end }: { metric: string; start: string; end: string } = $props();

	const uid = $props.id();
	let ignored = $state<Schemas['IgnoredSource'][] | null>(null);
	let problem = $state<Problem | null>(null);

	$effect(() => {
		const [m, s, e] = [metric, start, end];
		ignored = null;
		problem = null;
		void api.GET('/api/v1/sources/series', { params: { query: { metric: m, start: s, end: e, grain: 'day', include_ignored: true } } }).then((res) => {
			if (m !== metric || s !== start || e !== end) return;
			problem = res.error ?? null;
			if (res.data) ignored = res.data.ignored ?? [];
		});
	});

	const name = (o: Schemas['OriginRef']) => o.name ?? o.key ?? 'Unknown app';
	const day = (iso: string) => new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
</script>

<div class="ignored" role="region" aria-labelledby="{uid}-h" aria-live="polite">
	<p class="title" id="{uid}-h">Ignored sources</p>
	<ProblemAlert {problem} />
	{#if ignored === null}
		{#if !problem}<p class="muted">Loading ignored sources…</p>{/if}
	{:else if ignored.length === 0}
		<p class="muted">No records in this range are held raw by a source filter.</p>
	{:else}
		<ul>
			{#each ignored as i, n (n)}
				<li>
					<strong>{name(i.origin)}</strong>: {i.records.toLocaleString()} {i.records === 1 ? 'record' : 'records'} held raw, ignored by the device’s source filter
					<span class="muted">· {day(i.first_at)} – {day(i.last_at)}</span>
				</li>
			{/each}
		</ul>
	{/if}
</div>

<style>
	.ignored {
		display: grid;
		gap: var(--space-2);
		padding-top: var(--space-3);
		border-top: 1px solid var(--color-border);
		font-size: var(--text-sm);
	}
	.title {
		font-weight: 600;
	}
	p,
	ul {
		margin: 0;
	}
	ul {
		display: grid;
		gap: var(--space-1);
		padding-left: var(--space-5);
	}
</style>
