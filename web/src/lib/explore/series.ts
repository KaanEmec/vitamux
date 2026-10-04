// Turning resolved and per-source responses into chart series for the metric detail page.
// Local dates plot at their UTC midnight and the chart formats in UTC, so a date never shifts.
import type { Schemas } from '../api/client.ts';
import type { Series } from '../charts/types.ts';
import { providerLabel } from '../connections/connections.ts';

export const dayMs = (date: string) => Date.parse(`${date}T00:00:00Z`);

/** A resolved value as a number: itself, or the metric's entry of a family value. */
export function num(value: unknown, code: string): number | null {
	if (typeof value === 'number') return value;
	const v = value as Record<string, unknown> | null | undefined;
	return typeof v?.[code] === 'number' ? (v[code] as number) : null;
}

type Source = Schemas['SourceSeriesSource'];

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
