<!--
	Range presets for a chart (1D, 1W, 1M, 3M, 1Y, All); zooming further is a drag on the chart.
	rangeStart() turns a preset into the first local date of the range.
-->
<script lang="ts" module>
	import { addDays } from '../data/format.ts';

	export type RangeKey = '1D' | '1W' | '1M' | '3M' | '1Y' | 'All';

	const days: Record<RangeKey, number | null> = { '1D': 1, '1W': 7, '1M': 30, '3M': 90, '1Y': 365, All: null };

	/** First local date (YYYY-MM-DD) of the range ending on `end`, or null for All. */
	export function rangeStart(key: RangeKey, end: string): string | null {
		const n = days[key];
		return n == null ? null : addDays(end, 1 - n);
	}
</script>

<script lang="ts">
	import Segmented from '../ui/Segmented.svelte';

	let {
		value = $bindable('3M'),
		options = ['1D', '1W', '1M', '3M', '1Y', 'All'],
		labels = {},
		onchange
	}: { value?: RangeKey; options?: RangeKey[]; /** Text of a preset when it is not the key ("30D"). */ labels?: Partial<Record<RangeKey, string>>; onchange?: (key: RangeKey) => void } = $props();
</script>

<Segmented label="Range" options={options.map((k) => ({ value: k, label: labels[k] ?? k }))} bind:value {onchange} />
