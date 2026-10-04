// The local-date range behind a RangePicker preset: the last N days up to today.
import { rangeStart, type RangeKey } from '../charts/RangePicker.svelte';
import { today } from '../data/format.ts';

/** `start` is undefined for "All" (no lower bound). */
export function rangeDates(key: RangeKey): { start: string | undefined; end: string } {
	const end = today();
	return { start: rangeStart(key, end) ?? undefined, end };
}
