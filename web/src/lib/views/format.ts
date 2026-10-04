// Display helpers shared by the specialised views. Neutral wording only.
import { metricLabel } from '../data/format.ts';

const names: Record<string, string> = {
	apple_health: 'Apple Health',
	whoop: 'WHOOP',
	withings: 'Withings',
	garmin: 'Garmin',
	manual: 'Manual entry',
	push: 'Push',
	file_import: 'File import'
};

export const providerName = (provider: string) => names[provider] ?? metricLabel(provider);

/** "Apple Health · com.apple.health": a provider and, when known, its origin app or device. */
export function sourceLabel(provider: string, detail?: string | null): string {
	return detail ? `${providerName(provider)} · ${detail}` : providerName(provider);
}

/** Where a source stands under the rule, in words: "Selected", "In the rule", "Excluded: …", "Not in the rule". */
export function ruleTag(m: { selected: boolean; rule_status: 'used' | 'excluded' | 'not_in_rule'; reason?: string }): string {
	if (m.selected) return 'Selected';
	if (m.rule_status === 'used') return 'In the rule';
	if (m.rule_status === 'excluded') return `Excluded${m.reason ? `: ${m.reason}` : ''}`;
	return 'Not in the rule';
}

/** A source row's label: provider plus origin app, or device model, or device type. */
export function memberLabel(m: {
	provider: string;
	origin?: { name?: string } | null;
	device?: { model?: string; type?: string } | null;
}): string {
	const type = m.device?.type;
	return sourceLabel(m.provider, m.origin?.name ?? m.device?.model ?? (type && metricLabel(type)));
}

/** The label of a record's source (SourceRef): provider plus origin app or device type. */
export function recordLabel(s: { provider: string; origin: string | null; device_type: string | null }): string {
	return sourceLabel(s.provider, s.origin ?? (s.device_type && metricLabel(s.device_type)));
}

export const mean = (values: number[]): number | null => (values.length ? values.reduce((a, b) => a + b, 0) / values.length : null);

/** "7h 19m" for a duration in seconds, or an en dash when unknown. */
export function hm(seconds: number | null | undefined): string {
	if (seconds == null) return '–';
	const m = Math.round(seconds / 60);
	return `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, '0')}m`;
}

/** Midnight UTC of a local date: the x of a per-day point (charts then use timezone UTC for labels). */
export const dayMs = (date: string) => Date.parse(`${date}T00:00:00Z`);

const dayFormat = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });

/** "Sun, 4 Oct 2026" for a local date. */
export const dayLabel = (date: string) => dayFormat.format(dayMs(date));

/** The p-quantile (0 to 1) of the values, interpolated; null when there are none. */
export function quantile(values: number[], p: number): number | null {
	if (!values.length) return null;
	const s = values.toSorted((a, b) => a - b);
	const i = (s.length - 1) * p;
	const lo = Math.floor(i);
	return s[lo] + (s[Math.ceil(i)] - s[lo]) * (i - lo);
}
