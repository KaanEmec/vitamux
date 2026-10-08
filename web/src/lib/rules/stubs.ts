// Calls for the rules pages. Coverage is a typed stand-in while its schema is an open object in
// api/openapi.yaml. Both call the real spec path; a 404 or 503 means "not available yet", and the
// pages show a friendly empty state instead of an error.
import { api, type Problem, type Schemas } from '../api/client.ts';
import type { Rule } from './rule.ts';

const unavailable = (p: Problem) => p.status === 404 || p.status === 503;

// TODO(J10.5): replace with components['schemas']['Coverage'] once GET /coverage ships.
/** Source × day coverage: `days[i]` is the 0–1 coverage of `start_date + i`. */
export interface Coverage {
	start_date: string;
	end_date: string;
	rows: { metric: string; source: string; days: number[] }[];
}

/** GET /coverage, optionally for one metric and one origin app; null while the endpoint is not available. */
export async function getCoverage(start: string, end: string, metric?: string, origin?: string): Promise<Coverage | null> {
	const { data, error } = await api.GET('/api/v1/coverage', {
		params: { query: { start_date: start, end_date: end, metric: metric ? [metric] : undefined, origin: origin ? [origin] : undefined } }
	});
	if (error) return null;
	const c = data as unknown as Coverage;
	return Array.isArray(c?.rows) ? c : null;
}

/** POST /resolution/preview (J10.3): the draft rule and the rule in effect, per local date. */
export type PreviewDay = Schemas['PreviewDay'];
type Preview = Schemas['ResolutionPreview'];

export type PreviewOutcome = { preview: Preview } | { unavailable: true } | { problem: Problem };

/** POST /resolution/preview: the draft rule against the active one over [start, end]. */
export async function previewRule(spec: Rule, start: string, end: string): Promise<PreviewOutcome> {
	const { data, error } = await api.POST('/api/v1/resolution/preview', {
		body: { spec: spec as unknown as Schemas['ResolutionPreviewRequest']['spec'], start_date: start, end_date: end }
	});
	if (error) return unavailable(error) ? { unavailable: true } : { problem: error };
	return { preview: data };
}
