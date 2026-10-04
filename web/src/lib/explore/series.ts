// Turning resolved and per-source responses into chart series for the metric detail page, and
// the source behind each resolved day.
// Local dates plot at their UTC midnight and the chart formats in UTC, so a date never shifts.
import type { Schemas } from '../api/client.ts';
import type { Series } from '../charts/types.ts';
import { providerLabel } from '../connections/connections.ts';
import { dayMs } from '../views/format.ts';

/** A resolved value as a number: itself, or the metric's entry of a family value. */
export function num(value: unknown, code: string): number | null {
	if (typeof value === 'number') return value;
	const v = value as Record<string, unknown> | null | undefined;
	return typeof v?.[code] === 'number' ? (v[code] as number) : null;
}

type Source = Schemas['SourceSeriesSource'];
type Resolved = Schemas['ResolvedValue'];

/** The rule group a resolved day came from, or ''. */
export const dayGroup = (v: Resolved | undefined) => v?.selected ?? v?.inputs?.find((i) => i.selected)?.group ?? '';

/** The providers behind a resolved day (its selected inputs' sources), else its group; none without a value. */
export function dayProviders(v: Resolved | undefined): string[] {
	if (!v || v.status === 'no_data') return [];
	const used = (v.inputs ?? []).filter((i) => i.selected).flatMap((i) => (i.sources ?? []).map((s) => s.provider));
	if (used.length) return [...new Set(used)];
	return dayGroup(v) ? [dayGroup(v)] : [];
}

/** A resolved value's warning codes, with the rule group they concern. */
export const warningCodes = (v: Resolved | undefined) => (v?.warnings ?? []).map((w) => (w.group ? `${w.code} (${w.group})` : w.code));

export function sourceLabel(s: Source): string {
	const parts = [providerLabel(s.provider)];
	const detail = s.origin?.name ?? s.origin?.key ?? s.device?.model ?? s.device?.type;
	if (detail) parts.push(detail);
	if (s.rule_status === 'excluded') parts.push('excluded');
	if (s.rule_status === 'not_in_rule') parts.push('not in rule');
	return parts.join(' · ');
}

/** Each source's own daily values between `from` and `to` (inclusive local dates). */
export function sourceSeries(res: Schemas['SourceSeries'], from: string, to: string): Series[] {
	return res.sources.map((s) => {
		const points = s.points.filter((p) => p.local_date >= from && p.local_date <= to);
		return {
			label: sourceLabel(s),
			source: s.provider,
			xs: points.map((p) => dayMs(p.local_date)),
			ys: points.map((p) => p.daily_value ?? (res.aggregation === 'additive' ? p.sum : p.mean) ?? null)
		};
	});
}
