// Reading a draft preview (POST /resolution/preview): which days change and by how much.
import type { Schemas } from '../api/client.ts';
import { formatValue } from '../data/format.ts';
import type { PreviewDay } from './stubs.ts';

type Resolved = Schemas['ResolvedValue'];

/** The group the value came from, or ''. */
export const selectedGroup = (r: Resolved) => r.inputs?.find((i) => i.selected)?.group ?? '';

/** "61.5 bpm", or "no value". */
export const showResolved = (r: Resolved) => (r.value == null ? 'no value' : formatValue(r.value, r.unit));

export const numeric = (v: unknown): number | null => (typeof v === 'number' ? v : null);

/** The draft gives another value, status or source than the rule in effect. */
export const dayChanged = (d: PreviewDay) =>
	JSON.stringify(d.draft.value) !== JSON.stringify(d.active.value) ||
	d.draft.status !== d.active.status ||
	selectedGroup(d.draft) !== selectedGroup(d.active);

const mean = (xs: number[]) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : null);

/** Days that change, the shift of the mean (numbers only) and days that lose their value. */
export function summarize(days: PreviewDay[]) {
	const draft = mean(days.map((d) => numeric(d.draft.value)).filter((v) => v !== null));
	const active = mean(days.map((d) => numeric(d.active.value)).filter((v) => v !== null));
	return {
		changed: days.filter(dayChanged),
		shift: draft !== null && active !== null ? draft - active : null,
		newGaps: days.filter((d) => d.active.value != null && d.draft.value == null).length,
		unit: days.find((d) => d.draft.unit)?.draft.unit ?? ''
	};
}
