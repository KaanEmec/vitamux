// /data moved to Explore (J21.8): old links and bookmarks keep working. The session check of the
// (app) layout runs first, so a signed-out visitor still comes back to the old path.
import { redirect } from '@sveltejs/kit';
import type { PageLoad } from './$types';

export const load: PageLoad = async ({ params, url, parent }) => {
	await parent();
	redirect(308, target(params.path.split('/').filter(Boolean), url.searchParams));
};

function target([first, metric, date]: string[], q: URLSearchParams): string {
	const query = q.size ? `?${q}` : '';
	if (first === 'sleep' || first === 'workouts') return `/explore/${first}${query}`;
	if (first === 'day' && metric && date) return `/explore/${encodeURIComponent(metric)}/day/${encodeURIComponent(date)}`;
	const m = q.get('metric');
	if (!first && m) {
		const end = q.get('end');
		return `/explore/${encodeURIComponent(m)}${end ? `?end=${encodeURIComponent(end)}` : ''}`;
	}
	return '/explore';
}
