// Timezone helpers for timezone periods (an instant plus an IANA zone).

/** True when the browser knows `tz` as an IANA zone. */
export function validZone(tz: string): boolean {
	try {
		new Intl.DateTimeFormat('en', { timeZone: tz });
		return tz.trim() !== '';
	} catch {
		return false;
	}
}

/** Offset of `tz` from UTC in milliseconds at the instant `ms`. */
function offsetMs(tz: string, ms: number): number {
	const p = Object.fromEntries(
		new Intl.DateTimeFormat('en-US', {
			timeZone: tz,
			hourCycle: 'h23',
			year: 'numeric',
			month: '2-digit',
			day: '2-digit',
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit'
		})
			.formatToParts(new Date(ms))
			.map((x) => [x.type, x.value])
	);
	return Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour, +p.minute, +p.second) - Math.floor(ms / 1000) * 1000;
}

/**
 * The instant at which the wall clock in `tz` reads `local` ("YYYY-MM-DDTHH:mm"), as an
 * ISO string; null when `local` is not a date and time. A wall time that does not exist
 * (skipped by a DST change) resolves to an instant an hour off; the server shows the result.
 */
export function zonedInstant(local: string, tz: string): string | null {
	const m = local.match(/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/);
	if (!m) return null;
	const wall = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]);
	let guess = wall - offsetMs(tz, wall);
	guess = wall - offsetMs(tz, guess);
	return new Date(guess).toISOString();
}

/** "YYYY-MM-DDTHH:mm" wall-clock reading of an instant in `tz` (for editing a period). */
export function zonedLocal(iso: string, tz: string): string {
	const t = Date.parse(iso);
	const d = new Date(t + offsetMs(tz, t));
	return d.toISOString().slice(0, 16);
}

/** Readable "2026-03-01 00:00" of an instant in `tz`. */
export function zonedLabel(iso: string, tz: string): string {
	return zonedLocal(iso, tz).replace('T', ' ');
}

/** Every IANA zone the browser lists, for a datalist; empty when unsupported. */
export function zoneNames(): string[] {
	const f = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
	try {
		return f ? f('timeZone') : [];
	} catch {
		return [];
	}
}
