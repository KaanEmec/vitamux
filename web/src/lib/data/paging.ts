// Follows cursor pagination to the end. `page` returns one page or a problem; the first
// problem stops the walk and is returned with whatever was read so far.
import type { Problem } from '#lib/api/client.ts';

export interface Page<T> {
	items: T[];
	next?: string;
	problem?: Problem;
}

/** Safety cap so a misbehaving server cannot loop the browser forever. */
const maxPages = 50;

export async function readAll<T>(page: (cursor?: string) => Promise<Page<T>>): Promise<{ items: T[]; problem?: Problem }> {
	const items: T[] = [];
	let cursor: string | undefined;
	for (let i = 0; i < maxPages; i++) {
		const p = await page(cursor);
		items.push(...p.items);
		if (p.problem) return { items, problem: p.problem };
		if (!p.next) break;
		cursor = p.next;
	}
	return { items };
}
