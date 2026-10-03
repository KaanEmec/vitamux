// Every page in (app) needs a session: load it once, or send the visitor to the login
// page with the requested path so they come back after signing in.
import { error, redirect } from '@sveltejs/kit';
import { api } from '#lib/api/client.ts';
import { loginHref, session, setSession } from '#lib/session.svelte.ts';
import type { LayoutLoad } from './$types';

export const load: LayoutLoad = async ({ url }) => {
	if (session.user) return;
	const { data, error: problem, response } = await api.GET('/api/v1/auth/session');
	if (data) {
		setSession(data);
		return;
	}
	if (response.status === 401) redirect(307, loginHref(url.pathname + url.search));
	error(response.status, problem.detail || problem.title);
};
