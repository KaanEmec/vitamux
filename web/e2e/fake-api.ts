// A stateful stand-in for the auth endpoints of internal/api/auth.go, installed with
// page.route. Values are synthetic. Unstubbed /api calls answer 404 problem+json.
import { test as base, expect, type Page, type Route } from '@playwright/test';

export const owner = {
	username: 'owner',
	password: 'synthetic-password',
	totp: '123456',
	recovery: 'synthetic-recovery-1'
};
const csrfToken = 'synthetic-csrf';

export class FakeApi {
	signedIn = false;
	totpEnabled = false;
	/** Status for GET /system/version (the shell fetches it once); 401 simulates expiry. */
	versionStatus = 200;
	/** X-CSRF-Token values received on logout. */
	logoutTokens: (string | null)[] = [];

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/**', (r) => problem(r, 404, 'not_found', 'not stubbed in e2e'));
		await this.page.route('**/api/v1/auth/session', (r) =>
			this.signedIn ? json(r, 200, this.session()) : problem(r, 401, 'unauthenticated', 'sign in first')
		);
		await this.page.route('**/api/v1/auth/login', (r) => this.login(r));
		await this.page.route('**/api/v1/auth/logout', (r) => {
			const token = r.request().headers()['x-csrf-token'] ?? null;
			this.logoutTokens.push(token);
			if (!this.signedIn) return problem(r, 401, 'unauthenticated', 'sign in first');
			if (token !== csrfToken) return problem(r, 403, 'forbidden', 'missing or invalid X-CSRF-Token header');
			this.signedIn = false;
			return r.fulfill({ status: 204 });
		});
		await this.page.route('**/api/v1/system/version', (r) =>
			this.versionStatus === 200
				? json(r, 200, { version: '0.0.0-e2e', commit: 'e2e' })
				: problem(r, this.versionStatus, 'unauthenticated', 'session expired')
		);
	}

	private session() {
		return {
			user: { id: '00000000-0000-4000-8000-000000000001', username: owner.username, totp_enabled: this.totpEnabled },
			csrf_token: csrfToken
		};
	}

	private login(r: Route) {
		const body = r.request().postDataJSON() as Record<string, string>;
		if (body.username === 'bad name') {
			return problem(r, 422, 'validation_failed', 'invalid sign-in request', [
				{ pointer: '/username', detail: 'must not contain spaces' }
			]);
		}
		if (body.username !== owner.username || body.password !== owner.password) {
			return problem(r, 401, 'unauthenticated', 'invalid username, password or code');
		}
		if (this.totpEnabled) {
			if (!body.totp_code && !body.recovery_code) {
				return problem(r, 401, 'totp_required', 'enter the code from your authenticator app or a recovery code');
			}
			if (body.totp_code !== owner.totp && body.recovery_code !== owner.recovery) {
				return problem(r, 401, 'unauthenticated', 'invalid username, password or code');
			}
		}
		this.signedIn = true;
		return json(r, 200, this.session());
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}

function problem(r: Route, status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
	return r.fulfill({
		status,
		contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e', errors })
	});
}

// Chrome logs every non-2xx response, and SvelteKit's route announcer carries an inline
// style that the CSP blocks (base.css hides it instead). Anything else fails the test.
const announcerStyleHash = 'sha256-S8qMpvofolR8Mpjy4kQvEm7m1q8clzU4dfDH0AmvZjo=';
function expectedConsoleError(text: string) {
	return text.startsWith('Failed to load resource') || text.includes(announcerStyleHash);
}

/** `test` with a fresh FakeApi per test; fails the test on page errors or CSP violations. */
export const test = base.extend<{ api: FakeApi }>({
	api: [
		async ({ page }, use) => {
			const errors: string[] = [];
			page.on('pageerror', (e) => errors.push(e.message));
			page.on('console', (m) => {
				if (m.type() === 'error' && !expectedConsoleError(m.text())) errors.push(m.text());
			});
			const api = new FakeApi(page);
			await api.install();
			await use(api);
			expect(errors, 'page errors or CSP violations').toEqual([]);
		},
		{ auto: true }
	]
});

export { expect };
