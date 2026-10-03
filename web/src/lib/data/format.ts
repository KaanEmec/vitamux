// Display helpers for the Data section. Dates are local YYYY-MM-DD strings; instants stay RFC 3339.

/** "2026-09-14" for the browser's current local date. */
export function today(): string {
	const d = new Date();
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Adds `n` days to a YYYY-MM-DD date (calendar arithmetic, no timezone involved). */
export function addDays(date: string, n: number): string {
	const [y, m, d] = date.split('-').map(Number);
	const t = new Date(Date.UTC(y, m - 1, d + n));
	return `${t.getUTCFullYear()}-${pad(t.getUTCMonth() + 1)}-${pad(t.getUTCDate())}`;
}

/** Every date from `start` to `end` inclusive, newest first. */
export function datesDescending(start: string, end: string): string[] {
	const out: string[] = [];
	for (let d = end; d >= start && out.length < 400; d = addDays(d, -1)) out.push(d);
	return out;
}

export const isDate = (s: string | null | undefined): s is string => !!s && /^\d{4}-\d{2}-\d{2}$/.test(s);

function pad(n: number): string {
	return String(n).padStart(2, '0');
}

/** Clock time "HH:MM" of an instant at a UTC offset in minutes (the record's own offset). */
export function clock(iso: string, offsetMin: number | null | undefined): string {
	const t = new Date(Date.parse(iso) + (offsetMin ?? 0) * 60_000);
	return `${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())}`;
}

/** "h:mm" for a duration in seconds, or an en dash when unknown. */
export function duration(seconds: number | null | undefined): string {
	if (seconds == null) return '–';
	const m = Math.round(seconds / 60);
	return `${Math.floor(m / 60)}:${pad(m % 60)}`;
}

const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 1 });

/**
 * Renders a resolved value: a number, or an object of components (blood pressure becomes
 * "121/79", other components "name value").
 */
export function formatValue(value: unknown, unit?: string): string {
	if (value == null) return '–';
	const u = unit ? ` ${unit}` : '';
	if (typeof value === 'number') return number.format(value) + u;
	if (typeof value === 'object') {
		const v = value as Record<string, unknown>;
		if (typeof v.systolic === 'number' && typeof v.diastolic === 'number') {
			const pulse = typeof v.pulse === 'number' ? ` · pulse ${number.format(v.pulse)}` : '';
			return `${number.format(v.systolic)}/${number.format(v.diastolic)}${pulse}${u || ' mmHg'}`;
		}
		return Object.entries(v)
			.map(([k, x]) => `${k} ${typeof x === 'number' ? number.format(x) : String(x)}`)
			.join(', ');
	}
	return String(value) + u;
}

/** "steps" -> "Steps", "resting_heart_rate" -> "Resting heart rate". */
export function metricLabel(code: string): string {
	const s = code.replaceAll('_', ' ');
	return s.charAt(0).toUpperCase() + s.slice(1);
}
