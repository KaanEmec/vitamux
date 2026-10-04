// Reading a resolved night (GET /resolved/sleep) with the sessions behind it (GET /sleep).
import type { Schemas } from '../api/client.ts';

export type Night = Schemas['ResolvedNight'];
export type Session = Schemas['SleepSession'];
export type Member = Schemas['SleepMember'];

/** Seconds per sleep code (sleep_total, sleep_deep, …) of the night's result; empty when it has none. */
export function nightSeconds(n: Night): Record<string, number> {
	const v = n.result.value;
	return n.result.status !== 'no_data' && v && typeof v === 'object' ? (v as Record<string, number>) : {};
}

/** Hours of one sleep code in the night's result, or null when it has none. */
export function nightHours(n: Night, code: string): number | null {
	const s = nightSeconds(n)[code];
	return s == null ? null : s / 3600;
}

/** The selected source's sessions in the night's main episode. */
export function selectedSessions(n: Night, byId: Map<string, Session>): Session[] {
	return (n.members.find((m) => m.selected)?.session_refs ?? []).flatMap((id) => byId.get(id) ?? []);
}

/** Clock hours of the main episode: bed time, and wake time unwrapped so it may pass 24. */
export interface Span {
	bed: number;
	wake: number;
}

export function nightSpan(sessions: Session[]): Span | null {
	if (!sessions.length) return null;
	const start = Math.min(...sessions.map((s) => Date.parse(s.start_at)));
	const end = Math.max(...sessions.map((s) => Date.parse(s.end_at)));
	const local = new Date(start + (sessions[0].tz_offset_min ?? 0) * 60_000);
	const h = local.getUTCHours() + local.getUTCMinutes() / 60;
	// Before noon counts as after midnight, so 23:00 and 01:00 sit on one axis.
	const bed = h < 12 ? h + 24 : h;
	return { bed, wake: bed + (end - start) / 3_600_000 };
}

/** "23:12" for clock hours (values past 24 wrap to the next day). */
export function clockText(hours: number): string {
	const m = Math.round(hours * 60) % 1440;
	return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

/** Stages of a member's sessions in time order, for one hypnogram. */
export const memberStages = (sessions: Session[]) =>
	sessions.flatMap((s) => s.stages ?? []).sort((a, b) => Date.parse(a.start_at) - Date.parse(b.start_at));
