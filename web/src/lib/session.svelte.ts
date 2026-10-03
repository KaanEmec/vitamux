// The signed-in owner and the CSRF token for mutating requests. Filled by the (app)
// layout guard or the login page; cleared on logout and on any expired-session 401.
import type { components } from './api/schema';

type Session = components['schemas']['Session'];

export const session = $state<{ user: Session['user'] | null; csrf: string }>({ user: null, csrf: '' });

export function setSession(s: Session) {
	session.user = s.user;
	session.csrf = s.csrf_token;
}

export function clearSession() {
	session.user = null;
	session.csrf = '';
}

/** The login URL that returns to `next` (a path plus query) after signing in. */
export function loginHref(next: string, expired = false): string {
	const q = [];
	if (next && next !== '/') q.push(`next=${encodeURIComponent(next)}`);
	if (expired) q.push('reason=expired');
	return q.length ? `/login?${q.join('&')}` : '/login';
}

/** Returns `next` when it is a same-origin app path, otherwise "/" (no open redirects). */
export function safeNext(next: string | null): string {
	if (!next || !next.startsWith('/') || next.startsWith('//') || next.startsWith('/\\') || next.startsWith('/login')) {
		return '/';
	}
	return next;
}
