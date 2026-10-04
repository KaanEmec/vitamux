// The providers GET /providers lists (display names, availability), loaded once and shared like
// the session. Pages that show provider names call `loadProviders()`; until it answers, or when a
// provider is not in the list, labels fall back to the code (connections.ts providerLabel).
import { api, type Problem, type Schemas } from '../api/client.ts';

export const known = $state<{ list: Schemas['Provider'][] | null }>({ list: null });

let inflight: Promise<Problem | null> | null = null;

/** Fetches the list unless it is loaded; `refresh` fetches again (availability changes). Resolves to the error, if any. */
export function loadProviders(refresh = false): Promise<Problem | null> {
	if (known.list && !refresh) return Promise.resolve(null);
	inflight ??= api.GET('/api/v1/providers').then(({ data, error }) => {
		inflight = null;
		if (data) known.list = data.providers;
		return error ?? null;
	});
	return inflight;
}
