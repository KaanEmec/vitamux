// The one API client: openapi-fetch typed by the generated schema (npm run openapi).
//
//   const { data, error } = await api.GET('/api/v1/system/version');
//   if (error) problem = error; // always a Problem (see below), render with <ProblemAlert>
//
// The middleware sends X-CSRF-Token on mutating requests, turns any 401 outside the auth
// endpoints into a redirect to the login page (keeping the current path), and normalises
// every failure into problem+json so callers handle exactly one error shape.
import createClient, { type Middleware } from 'openapi-fetch';
import { goto } from '$app/navigation';
import { clearSession, loginHref, session } from '../session.svelte.ts';
import type { components, paths } from './schema';

export type Schemas = components['schemas'];
export type Problem = Schemas['Problem'];

const safeMethods = new Set(['GET', 'HEAD', 'OPTIONS']);
// A 401 from these is an answer (wrong password, no session yet), not an expiry.
const authPaths = new Set(['/api/v1/auth/login', '/api/v1/auth/session', '/api/v1/auth/logout']);

const middleware: Middleware = {
	onRequest({ request }) {
		if (!safeMethods.has(request.method) && session.csrf) {
			request.headers.set('X-CSRF-Token', session.csrf);
		}
		return request;
	},
	onResponse({ response, schemaPath }) {
		if (response.status === 401 && !authPaths.has(schemaPath)) sessionExpired();
		if (response.ok || response.headers.get('Content-Type')?.startsWith('application/problem+json')) {
			return response;
		}
		// Proxies and the dev server can answer with HTML or plain text.
		return problemResponse(response.status, response.statusText || 'Request failed');
	},
	onError() {
		return problemResponse(503, 'Server unreachable', 'Check that Vitamux is running and try again.');
	}
};

export const api = createClient<paths>({ baseUrl: '' });
api.use(middleware);

function problemResponse(status: number, title: string, detail?: string): Response {
	const problem: Problem = {
		type: 'about:blank',
		title,
		status,
		detail: detail ?? `The server answered with status ${status}.`,
		code: status >= 500 ? 'unavailable' : status === 401 ? 'unauthenticated' : 'unexpected_response'
	};
	return new Response(JSON.stringify(problem), {
		status,
		headers: { 'Content-Type': 'application/problem+json' }
	});
}

function sessionExpired() {
	clearSession();
	if (location.pathname === '/login') return;
	void goto(loginHref(location.pathname + location.search, true), { replaceState: true });
}

/**
 * Maps a problem's field errors to input names: pointer "/username" becomes "username",
 * "/rows/3/value" becomes "rows.3.value". Several errors on one field are joined.
 */
export function fieldErrors(problem: Problem | null | undefined): Record<string, string> {
	const out: Record<string, string> = {};
	for (const e of problem?.errors ?? []) {
		const key = e.pointer.replace(/^\//, '').replaceAll('/', '.');
		out[key] = out[key] ? `${out[key]}; ${e.detail}` : e.detail;
	}
	return out;
}
