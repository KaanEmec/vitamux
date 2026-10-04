// Reading a resolved night (GET /resolved/sleep) with the sessions behind it (GET /sleep).
import type { Schemas } from '../api/client.ts';
import { stageOrder } from '../charts/sleep.ts';
import { addDays, clock } from '../data/format.ts';
import { dayMs } from './format.ts';

export type Night = Schemas['ResolvedNight'];
export type Session = Schemas['SleepSession'];
export type Member = Schemas['SleepMember'];

/** Seconds per sleep code (sleep_total, sleep_deep, …) of the night's result; empty when it has none. */
export function nightSeconds(n: Night): Record<string, number> {
	const v = n.result.value;
	return n.result.status !== 'no_data' && v && typeof v === 'object' ? (v as Record<string, number>) : {};
}

/** The four stages a night is broken down into, in display order. */
export const nightStages = ['deep', 'rem', 'light', 'awake'] as const;

/** Seconds in each of the four stages of the night's result (0 when absent). */
export const stageSeconds = (n: Night) => nightStages.map((stage) => ({ stage, seconds: nightSeconds(n)[`sleep_${stage}`] ?? 0 }));

/** Hours of one sleep code in the night's result, or null when it has none. */
export function nightHours(n: Night, code: string): number | null {
	const s = nightSeconds(n)[code];
	return s == null ? null : s / 3600;
}

/** The sessions behind a member (one source's episode). */
export const memberSessions = (m: Member | undefined, byId: Map<string, Session>): Session[] =>
	(m?.session_refs ?? []).flatMap((id) => byId.get(id) ?? []);

/** The sessions of the selected source's main episode. */
export const selectedSessions = (n: Night, byId: Map<string, Session>) => memberSessions(n.members.find((m) => m.selected), byId);

/** The sessions with stages, and the stage rows and time axis that line them up on hypnograms. */
export function stageAxis(sessions: Session[]) {
	const staged = sessions.filter((s) => s.stages?.length);
	return {
		staged,
		from: Math.min(...staged.map((s) => Date.parse(s.start_at))),
		to: Math.max(...staged.map((s) => Date.parse(s.end_at))),
		rows: stageOrder.filter((st) => staged.some((s) => s.stages?.some((x) => x.stage === st)))
	};
}

/** "23:12 to 07:01": the clock span of sessions in time order, at the first one's offset. */
export const sessionsSpan = (ss: Session[]) => `${clock(ss[0].start_at, ss[0].tz_offset_min)} to ${clock(ss.at(-1)?.end_at ?? ss[0].end_at, ss[0].tz_offset_min)}`;

/** Clock hours of the main episode: bed time, and wake time unwrapped so it may pass 24. */
export interface Span {
	bed: number;
	wake: number;
}

/** The night's episode (bed to wake) on one clock axis, in the owner's timezone; null without an episode. */
export function episodeSpan(n: Night, timeZone?: string): Span | null {
	if (!n.episode) return null;
	const start = Date.parse(n.episode.start);
	const parts = new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', hour: 'numeric', minute: 'numeric' }).formatToParts(start);
	const at = (type: string) => Number(parts.find((p) => p.type === type)?.value ?? 0);
	const h = at('hour') + at('minute') / 60;
	// Before noon counts as after midnight, so 23:00 and 01:00 sit on one axis.
	const bed = h < 12 ? h + 24 : h;
	return { bed, wake: bed + (Date.parse(n.episode.end) - start) / 3_600_000 };
}

/** "23:12" for clock hours (values past 24 wrap to the next day). */
export function clockText(hours: number): string {
	const m = Math.round(hours * 60) % 1440;
	return `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`;
}

/** Stages of a member's sessions in time order, for one hypnogram. */
export const memberStages = (sessions: Session[]) =>
	sessions.flatMap((s) => s.stages ?? []).sort((a, b) => Date.parse(a.start_at) - Date.parse(b.start_at));

const dayParts = new Intl.DateTimeFormat(undefined, { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' });

/** "Sun 4" or "Sun 4 Oct" for a local date: the same order in every locale. */
function day(date: string, month: boolean): string {
	const p = Object.fromEntries(dayParts.formatToParts(dayMs(date)).map((x) => [x.type, x.value]));
	return `${p.weekday} ${p.day}${month ? ` ${p.month}` : ''}`;
}

/** "Sat 3 → Sun 4 Oct": a night is dated by the day you woke up. */
export const nightLabel = (date: string) => `${day(addDays(date, -1), false)} → ${day(date, true)}`;

/** "Sun 4 Oct" for a local date. */
export const shortDay = (date: string) => day(date, true);
