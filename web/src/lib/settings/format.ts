// Display helpers for the Settings section.

/** "12.4 MiB" for a byte count; an en dash when unknown. */
export function bytes(n: unknown): string {
	if (typeof n !== 'number' || !Number.isFinite(n) || n < 0) return '–';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
	let v = n;
	let i = 0;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

/** Local date and time of an RFC 3339 instant; an en dash when missing or invalid. */
export function when(iso: string | null | undefined): string {
	const t = iso ? new Date(iso) : null;
	return t && !Number.isNaN(t.getTime()) ? t.toLocaleString() : '–';
}

/** "3 days ago" style age of an instant, or '' when unknown. */
export function ago(iso: string | null | undefined, now = Date.now()): string {
	const t = iso ? Date.parse(iso) : NaN;
	if (Number.isNaN(t)) return '';
	const mins = Math.max(0, Math.round((now - t) / 60_000));
	if (mins < 60) return `${mins} min ago`;
	if (mins < 2 * 24 * 60) return `${Math.round(mins / 60)} h ago`;
	return `${Math.round(mins / (24 * 60))} days ago`;
}
