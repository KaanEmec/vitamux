// GET /system/status is an open object whose properties J10.5 adds, so it is read
// defensively: every field is optional, unknown shapes are ignored or shown generically,
// and a missing endpoint (404, 501, 503) just means "not available".
import { api, type Problem } from '#lib/api/client.ts';

export type Obj = Record<string, unknown>;

export const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v);

export async function loadStatus(): Promise<{ status: Obj | null; problem: Problem | null }> {
	const { data, error } = await api.GET('/api/v1/system/status');
	if (error) return { status: null, problem: error };
	return { status: isObj(data) ? data : {}, problem: null };
}

/** First defined value among the dotted `paths` ("backup.last_at") of `o`. */
export function pick(o: Obj | null, ...paths: string[]): unknown {
	for (const p of paths) {
		let v: unknown = o;
		for (const k of p.split('.')) v = isObj(v) ? v[k] : undefined;
		if (v !== undefined && v !== null) return v;
	}
	return undefined;
}

export const asNumber = (v: unknown): number | undefined => (typeof v === 'number' ? v : undefined);

/** RFC 3339 instant of the last backup, whether the status carries a string or an object. */
export function lastBackup(status: Obj | null): string | null {
	const v = pick(status, 'last_backup', 'last_backup_at', 'backup', 'backups.last');
	if (typeof v === 'string') return v;
	const at = pick(isObj(v) ? v : null, 'at', 'finished_at', 'completed_at', 'created_at', 'time', 'last_at');
	return typeof at === 'string' ? at : null;
}

/** A list of objects under any of `paths`; non-object entries are dropped. */
export function list(status: Obj | null, ...paths: string[]): Obj[] {
	const v = pick(status, ...paths);
	return Array.isArray(v) ? v.filter(isObj) : [];
}

/** "key: value" pairs of an entry's scalar fields, for generic display. */
export function facts(o: Obj): { key: string; value: string }[] {
	return Object.entries(o)
		.filter(([, v]) => ['string', 'number', 'boolean'].includes(typeof v))
		.map(([key, v]) => ({ key, value: String(v) }));
}
