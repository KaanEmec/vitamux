<!--
	Events on a shared time axis, one lane per source or event type (workouts, sessions,
	notifications), labelled on the left. Each event is a bar in its lane's source colour; the
	arrow keys and the pointer move from event to event in time order, and the events are also
	available as a table.
-->
<script lang="ts">
	import { sourceClass } from '../ui/source.ts';
	import ChartFrame from './ChartFrame.svelte';
	import { formatInstant, nearest } from './scale.ts';

	let {
		lanes,
		from,
		to,
		label,
		timezone
	}: {
		lanes: { label: string; source?: string; events: { start: number; end: number; label: string }[] }[];
		from: number;
		to: number;
		label: string;
		timezone?: string;
	} = $props();

	const n = $derived(lanes.length);
	const events = $derived(
		lanes.flatMap((l, lane) => l.events.map((e) => ({ ...e, lane }))).sort((a, b) => a.start - b.start || a.lane - b.lane)
	);
	const mids = $derived(events.map((e) => (e.start + e.end) / 2));
	const short = (s: string) => (s.length > 24 ? `${s.slice(0, 23)}…` : s);
	const pick = (t: number) => {
		const i = events.findIndex((e) => e.start <= t && t <= e.end);
		return i >= 0 ? i : nearest(mids, t);
	};
	const tip = (i: number) => {
		const e = events[i];
		return {
			title: e.end > e.start ? `${formatInstant(e.start, timezone)} – ${formatInstant(e.end, timezone)}` : formatInstant(e.start, timezone),
			lead: { value: e.label },
			rows: [{ label: 'Lane', value: lanes[e.lane].label, source: lanes[e.lane].source }]
		};
	};
	const table = () => ({
		columns: ['Lane', 'Event', 'Start', 'End'],
		rows: lanes.flatMap((l) => l.events.map((e) => [l.label, e.label, formatInstant(e.start, timezone), formatInstant(e.end, timezone)]))
	});
</script>

<ChartFrame
	{label}
	xs={mids}
	x={[from, to]}
	y={[0, n]}
	{timezone}
	height={n * 28 + 38}
	padding={{ left: 168 }}
	crosshair={false}
	{pick}
	{tip}
	{table}
	yTicks={lanes.map((_, i) => n - i - 0.5)}
	yFormat={(v) => short(lanes[Math.round(n - 0.5 - v)]?.label ?? '')}
>
	{#snippet marks(f)}
		{@const h = f.sy(0) - f.sy(1)}
		{#each events as e, i (i)}
			{@const w = Math.max(f.sx(e.end) - f.sx(e.start), 3)}
			<rect
				class={['event', lanes[e.lane].source ? sourceClass(lanes[e.lane].source ?? '') : 'hue', f.active === i && 'active']}
				x={f.sx(e.start) - (w === 3 ? 1.5 : 0)}
				y={f.sy(n - e.lane) + h * 0.2}
				width={w}
				height={h * 0.6}
				rx="3"
			/>
		{/each}
	{/snippet}
</ChartFrame>

<style>
	.event {
		fill: var(--src);
	}
	.hue {
		--src: var(--metric, var(--color-accent));
	}
	.event.active {
		stroke: var(--color-text);
		stroke-width: 1.5;
	}
</style>
