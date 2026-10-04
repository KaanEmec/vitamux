<!-- The average night of a period: the mean time in each stage, and the range of its share of the night. -->
<script lang="ts">
	import StageStack from '../charts/StageStack.svelte';
	import { stageColor, stageLabels } from '../charts/sleep.ts';
	import { hm, mean } from './format.ts';
	import { nightSeconds, nightStages, type Night } from './sleep.ts';

	let { nights, total }: { /** Nights with data. */ nights: Night[]; /** Nights in the period. */ total: number } = $props();

	const average = $derived.by(() => {
		const per = nights.map(nightSeconds).filter((s) => nightStages.some((st) => s[`sleep_${st}`] != null));
		const shares = (st: string) => per.flatMap((s) => (s[`sleep_${st}`] != null ? s[`sleep_${st}`] / nightStages.reduce((n, o) => n + (s[`sleep_${o}`] ?? 0), 0) : []));
		const rows = nightStages.map((st) => {
			const sh = shares(st);
			return { stage: st, seconds: mean(per.flatMap((s) => s[`sleep_${st}`] ?? [])) ?? 0, share: mean(sh) ?? 0, lo: Math.min(...sh), hi: Math.max(...sh) };
		});
		const top = Math.max(0.1, Math.ceil(Math.max(...rows.map((r) => (Number.isFinite(r.hi) ? r.hi : 0))) * 10) / 10);
		return { rows: per.length ? rows : [], top };
	});
	const percent = (v: number) => `${Math.round(v * 100)}%`;
</script>

<section class="card" aria-labelledby="avg-h">
	<h2 id="avg-h">Average night</h2>
	<p class="muted note">{nights.length} of {total} nights</p>
	{#if average.rows.length}
		<StageStack stages={average.rows} label="Average night by stage" legend={false} />
		<ul>
			{#each average.rows as r (r.stage)}
				<li>
					<span class="row"><span class="name"><span class={['swatch', stageColor(r.stage)]}></span>{stageLabels[r.stage]}</span><span class="num">{hm(r.seconds)} <span class="muted">· {percent(r.share)}</span></span></span>
					<span class="track">
						<span class={['range', stageColor(r.stage)]} style:left="{(r.lo / average.top) * 100}%" style:width="{Math.max(2, ((r.hi - r.lo) / average.top) * 100)}%"></span>
						<span class={['tick', stageColor(r.stage)]} style:left="{(r.share / average.top) * 100}%"></span>
					</span>
				</li>
			{/each}
		</ul>
		<p class="muted note">Bar = range of its share of the night across the period, tick = mean.</p>
	{/if}
</section>

<style>
	h2 {
		margin-bottom: var(--space-1);
	}
	.note {
		margin: 0 0 var(--space-3);
		font-size: var(--text-sm);
	}
	ul {
		display: grid;
		gap: var(--space-3);
		margin: var(--space-4) 0 var(--space-3);
		padding: 0;
		list-style: none;
	}
	.row {
		display: flex;
		justify-content: space-between;
		gap: var(--space-2);
		margin-bottom: var(--space-2);
		font-size: var(--text-sm);
	}
	.name {
		display: inline-flex;
		align-items: center;
		gap: var(--space-2);
	}
	.swatch {
		width: 0.5rem;
		height: 0.5rem;
		border-radius: 2px;
	}
	.track {
		position: relative;
		display: block;
		height: 0.375rem;
		background: var(--chart-grid);
		border-radius: var(--radius-pill);
	}
	.range {
		position: absolute;
		top: 0;
		bottom: 0;
		border-radius: var(--radius-pill);
		opacity: 0.4;
	}
	.tick {
		position: absolute;
		top: -3px;
		width: 3px;
		height: 0.75rem;
		margin-left: -1.5px;
		border-radius: 2px;
	}
	.deep {
		background: var(--stage-deep);
	}
	.light {
		background: var(--stage-light);
	}
	.rem {
		background: var(--stage-rem);
	}
	.awake {
		background: var(--stage-awake);
	}
</style>
