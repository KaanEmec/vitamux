// The Apple Health source filter of a paired device (J22.25): which apps' data the phone takes.
// PUT /devices/{id}/source-filter takes the full list of explicit choices; an app left out
// follows its default again (apple-health.md#source-filter).
import type { Schemas } from '#lib/api/client.ts';
import { typeLabel } from './format.ts';

export type FilterOrigin = Schemas['SourceFilterOrigin'];
export type Choice = Schemas['SourceFilterChoice'];
export type Mode = Schemas['SourceFilterMode'];
export type Next = { mode: Mode; types?: string[] };

/** The app's name as Apple Health shows it, else its bundle id. */
export const appName = (o: Pick<FilterOrigin, 'name' | 'bundle_id'>) => o.name || o.bundle_id;

/** Every type the app writes, plus any taken type it has not reported (per_type). */
export function typesOf(o: FilterOrigin): string[] {
	return [...new Set([...o.writes.map((w) => w.type), ...(o.mode === 'per_type' ? o.types : [])])];
}

/** The types the device takes from the app; null means all of them. */
function taken(o: Pick<FilterOrigin, 'mode' | 'types'>): Set<string> | null {
	if (o.mode === 'take') return null;
	return new Set(o.mode === 'per_type' ? o.types : []);
}

/** A per-type selection as a choice: every type is take, none is ignore. */
export function fromTypes(all: string[], checked: string[]): Next {
	if (checked.length === 0) return { mode: 'ignore' };
	if (all.every((t) => checked.includes(t))) return { mode: 'take' };
	return { mode: 'per_type', types: all.filter((t) => checked.includes(t)) };
}

function choice(o: FilterOrigin, next: Next): Choice {
	return { bundle_id: o.bundle_id, ...(o.name ? { name: o.name } : {}), mode: next.mode, ...(next.mode === 'per_type' ? { types: next.types ?? [] } : {}) };
}

/** The full explicit list after changing one app; `next` null returns it to its default. */
export function withChoice(origins: FilterOrigin[], bundleId: string, next: Next | null): Choice[] {
	const out: Choice[] = [];
	for (const o of origins) {
		if (o.bundle_id === bundleId) {
			if (next) out.push(choice(o, next));
		} else if (o.explicit) out.push(choice(o, o));
	}
	return out;
}

/** "Taken", "Ignored" or the per-type list. */
export function modeText(o: FilterOrigin): string {
	if (o.mode === 'take') return 'Taken';
	if (o.mode === 'ignore') return 'Ignored';
	return `Takes only ${o.types.map(typeLabel).join(', ') || 'no types'}`;
}

/** The confirmation after a change: what the device now does with the app, in plain words. */
export function changeNotice(before: FilterOrigin, after: FilterOrigin): string {
	const name = appName(after);
	const was = taken(before);
	const now = taken(after);
	// Something newly taken: the server queued an anchor reset, so the phone reads that history.
	const pulls = was !== null && (now === null ? true : [...now].some((t) => !was.has(t)));
	const stops = after.mode === 'ignore' && before.mode !== 'ignore';
	let text: string;
	if (!after.explicit) text = `${name} follows its default again: ${after.mode === 'take' ? 'taken' : after.mode === 'ignore' ? 'ignored' : 'some types'}.`;
	else if (after.mode === 'take') text = `${name} is now taken.`;
	else if (after.mode === 'ignore') text = `${name} is now ignored.`;
	else text = `${name} is now taken for ${after.types.map(typeLabel).join(', ')} only.`;
	if (pulls) text += ' The phone pulls its history on its next sync.';
	if (stops) text += ' The phone stops sending its data on its next sync.';
	return text;
}
