// A connection's sync runs over the last 14 days, for the run strip (RunStrip.svelte).
import { api, type Schemas } from '../api/client.ts';

export type Run = Schemas['Run'];
export interface RunDay {
	/** Local date, YYYY-MM-DD. */
	date: string;
	succeeded: number;
	failed: number;
}

export const DAYS = 14;
const pageSize = 500;
const maxPages = 4;

const pad = (n: number) => String(n).padStart(2, '0');
const localDate = (d: Date) => `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;

function firstDay(now: number): Date {
	const d = new Date(now);
	d.setHours(0, 0, 0, 0);
	d.setDate(d.getDate() - (DAYS - 1));
	return d;
}

/** The runs of the last 14 local days, newest first, or null when the API does not answer. */
export async function loadRuns(id: string, now = Date.now()): Promise<Run[] | null> {
	const since = firstDay(now).getTime();
	const out: Run[] = [];
	let cursor: string | undefined;
	for (let page = 0; page < maxPages; page++) {
		const { data, error } = await api.GET('/api/v1/connections/{id}/runs', {
			params: { path: { id }, query: { limit: pageSize, cursor } }
		});
		if (error) return null;
		out.push(...data.runs);
		const oldest = data.runs.at(-1);
		if (!data.has_more || !data.next_cursor || !oldest || Date.parse(oldest.started_at) < since) break;
		cursor = data.next_cursor;
	}
	return out.filter((r) => Date.parse(r.started_at) >= since);
}

/** One entry per local day, oldest first, counting finished runs by outcome. Retries (rescheduled) are not counted. */
export function runDays(runs: Run[], now = Date.now()): RunDay[] {
	const start = firstDay(now);
	const days: RunDay[] = Array.from({ length: DAYS }, (_, i) => {
		const d = new Date(start);
		d.setDate(d.getDate() + i);
		return { date: localDate(d), succeeded: 0, failed: 0 };
	});
	const byDate = new Map(days.map((d) => [d.date, d]));
	for (const r of runs) {
		const d = byDate.get(localDate(new Date(r.started_at)));
		if (!d) continue;
		if (r.outcome === 'succeeded') d.succeeded++;
		else if (r.outcome === 'failed') d.failed++;
	}
	return days;
}
