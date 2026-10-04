// A stateful stand-in for guided source setup (E20, internal/api/setup.go), on top of fake-api.ts.
// Values are synthetic. Seed: a fresh install with no connection; Withings without app credentials,
// the bundled Garmin sidecar off (Compose and Coolify enable lines), WHOOP running, one sidecar of the
// environment. Each test adjusts the public fields first. auth/begin for Withings answers a redirect
// straight back to /connections?connected=withings (what the callback would do); Garmin asks for
// the sign-in, then a code. `failNext` makes the next auth/continue fail like the server would.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;

export const garminLogin = { email: 'synthetic@example.test', password: 'synthetic-pass', code: '654321' };
export const withingsApp = { id: 'synthetic-client-id', secret: 'synthetic-client-secret', wrong: 'synthetic-wrong-secret' };
export const sidecarSecret = 'f'.repeat(64);
const t0 = '2026-10-01T08:00:00Z';
const upstream = { package: 'garminconnect', version: '0.3.17', source_url: 'https://github.com/cyberjunky/python-garminconnect' };
const callback = 'https://vitamux.example.test/oauth/withings/callback';

interface Provider {
	code: string;
	name: string;
	official: boolean;
	auth_kind: string | null;
	remote: boolean;
	available: boolean;
	upstream?: typeof upstream;
	setup_state: string;
	callback_url: string | null;
	problems: { code: string; message: string }[];
	app_credentials: { set: boolean; managed_by_environment: boolean; client_id: string | null; updated_at: string | null } | null;
	sidecar: { source: string; bundled: boolean; enable: { install: string; line: string; apply: string }[] } | null;
	connections: number;
}

const unreachable = { code: 'sidecar_unreachable', message: 'The garmin sidecar is not running. Turn it on as shown, then check again.' };
const enable = [
	{ install: 'compose', line: 'COMPOSE_PROFILES=garmin', apply: 'docker compose up -d' },
	{ install: 'coolify', line: 'GARMIN_SIDECAR=1', apply: 'Redeploy the resource in Coolify' }
];

export class SetupApi {
	withings: Provider = {
		code: 'withings', name: 'Withings', official: true, auth_kind: 'oauth2', remote: false, available: true, setup_state: 'needs_app_credentials',
		callback_url: callback, problems: [], app_credentials: { set: false, managed_by_environment: false, client_id: null, updated_at: null }, sidecar: null, connections: 0
	};
	garmin: Provider = {
		code: 'garmin', name: 'Garmin Connect', official: false, auth_kind: null, remote: true, available: false, setup_state: 'needs_sidecar',
		callback_url: null, problems: [unreachable], app_credentials: null, sidecar: { source: 'environment', bundled: true, enable }, connections: 0
	};
	whoop: Provider = {
		code: 'whoop', name: 'WHOOP', official: false, auth_kind: 'interactive_mfa', remote: true, available: true, setup_state: 'ready',
		upstream: { package: '@dofek/whoop', version: '0.1.65', source_url: 'https://github.com/Asherlc/dofek' },
		callback_url: null, problems: [], app_credentials: null, sidecar: { source: 'environment', bundled: true, enable: [] }, connections: 0
	};
	sidecars: Json[] = [
		{ name: 'garmin', url: 'http://garmin:8080', source: 'environment', bundled: true, available: false, created_at: null },
		{ name: 'whoop', url: 'http://whoop:8080', source: 'environment', bundled: true, available: true, created_at: null }
	];
	connections: Json[] = [];
	/** Probes that still find the Garmin sidecar off; the next one after them finds it running. */
	probesUntilUp = 1;
	/** Makes the next auth/continue fail: 422 refused, 429 rate limited, 503 sidecar gone. */
	failNext: 422 | 429 | 503 | null = null;
	/** DELETE …/app-credentials without confirm answers 409 while this is true. */
	appInUse = false;
	/** Every request body, by "METHOD path", to check what the panel sent. */
	sent: { route: string; body: unknown }[] = [];
	begins: string[] = [];
	private pending = new Map<string, 'login' | 'code'>();
	private appSecret = '';
	private next = 1;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	providers() {
		return [this.withings, this.garmin, this.whoop];
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const method = r.request().method();
		const body = r.request().postData() ? r.request().postDataJSON() : null;
		if (method !== 'GET') this.sent.push({ route: `${method} ${path}${url.search}`, body });
		let m: RegExpMatchArray | null;
		if (path === '/providers') return json(r, 200, { providers: this.providers() });
		if (path === '/connections') return json(r, 200, { connections: this.connections });
		if ((m = path.match(/^\/connections\/(conn_[0-9a-f]{32})$/))) {
			const c = this.connections.find((x) => x.id === m![1]);
			return c ? json(r, 200, c) : problem(r, 404, 'not_found', 'no such connection');
		}
		if (path.match(/^\/connections\/[^/]+\/runs$/)) return json(r, 200, { runs: [], has_more: false });
		if (path.match(/^\/connections\/[^/]+\/backfills$/)) return json(r, 200, { backfills: [] });
		if (path === '/sidecars' && method === 'GET') return json(r, 200, { sidecars: this.sidecars });
		if (path === '/sidecars' && method === 'POST') return this.addSidecar(r, body as { name: string; url: string });
		if ((m = path.match(/^\/sidecars\/([^/]+)$/)) && method === 'DELETE') return this.removeSidecar(r, m[1], url.searchParams.get('confirm') === 'true');
		if (!(m = path.match(/^\/providers\/([^/]+)\/(.+)$/))) return r.fallback();
		const p = this.providers().find((x) => x.code === m![1]);
		if (!p) return problem(r, 404, 'not_found', 'no such provider');
		switch (`${method} ${m[2]}`) {
			case 'PUT app-credentials':
				return this.putApp(r, p, body as { client_id: string; client_secret: string });
			case 'DELETE app-credentials':
				return this.deleteApp(r, p, url.searchParams.get('confirm') === 'true');
			case 'POST app-credentials/verify':
				return json(r, 200, this.appSecret === withingsApp.secret
					? { result: 'valid', message: 'The provider accepted the client id and secret.' }
					: { result: 'invalid', message: 'The provider refused the client id and secret. Copy both again from its developer dashboard.' });
			case 'POST probe':
				return this.probe(r, p);
			case 'POST auth/begin':
				return this.begin(r, p);
			case 'POST auth/continue':
				return this.continueAuth(r, p, body as { state: string; values: Record<string, string> });
		}
		return r.fallback();
	}

	private putApp(r: Route, p: Provider, body: { client_id: string; client_secret: string }) {
		if (p.app_credentials?.managed_by_environment) return problem(r, 409, 'conflict', 'set by the environment');
		this.appSecret = body.client_secret;
		p.app_credentials = { set: true, managed_by_environment: false, client_id: body.client_id, updated_at: new Date().toISOString() };
		if (p.setup_state === 'needs_app_credentials') p.setup_state = 'ready';
		return json(r, 200, p);
	}

	private deleteApp(r: Route, p: Provider, confirm: boolean) {
		if (this.appInUse && !confirm) return problem(r, 409, 'conflict', 'connections use these app credentials; confirm to remove them');
		p.app_credentials = { set: false, managed_by_environment: false, client_id: null, updated_at: null };
		p.setup_state = 'needs_app_credentials';
		return r.fulfill({ status: 204 });
	}

	private probe(r: Route, p: Provider) {
		if (p === this.garmin && this.probesUntilUp-- <= 0) {
			Object.assign(p, { available: true, auth_kind: 'interactive_mfa', upstream, setup_state: 'ready', problems: [] });
			p.sidecar!.enable = [];
			Object.assign(this.sidecars[0], { available: true });
		}
		return json(r, 200, p);
	}

	private begin(r: Route, p: Provider) {
		this.begins.push(p.code);
		if (!p.available) return problem(r, 503, 'unavailable', 'authorization is not available for this provider');
		if (p.auth_kind === 'interactive_mfa') return json(r, 200, this.prompt('login', p.name));
		this.connect(p);
		return json(r, 200, { redirect_url: `/connections?connected=${p.code}` });
	}

	private prompt(step: 'login' | 'code', name: string) {
		const state = `state-${this.next++}`;
		this.pending.set(state, step);
		return step === 'login'
			? { state, prompt: { message: `Sign in to ${name} (unofficial access with your own account).`, fields: [{ name: 'username', label: 'Email', kind: 'text' }, { name: 'password', label: 'Password', kind: 'password' }] } }
			: { state, prompt: { message: `Enter the verification code ${name} sent you.`, fields: [{ name: 'code', label: 'Verification code', kind: 'code' }] } };
	}

	private continueAuth(r: Route, p: Provider, body: { state: string; values: Record<string, string> }) {
		const step = this.pending.get(body.state);
		this.pending.delete(body.state);
		const fail = this.failNext;
		this.failNext = null;
		if (!step) return problem(r, 422, 'validation_failed', 'the authorization expired or was already used; begin again', [{ pointer: '/state', detail: 'invalid, expired or used' }]);
		if (fail === 429) {
			return r.fulfill({
				status: 429, contentType: 'application/problem+json', headers: { 'Retry-After': '120' },
				body: JSON.stringify({ type: 'about:blank', title: 'rate_limited', status: 429, code: 'rate_limited', detail: 'the provider is limiting sign-in attempts; wait, then begin again' })
			});
		}
		if (fail === 503) {
			Object.assign(p, { available: false, setup_state: 'needs_sidecar', problems: [unreachable] });
			p.sidecar!.enable = enable;
			return problem(r, 503, 'unavailable', 'authorization is not available for this provider');
		}
		const v = body.values;
		const wrong = step === 'login' ? v.username !== garminLogin.email || v.password !== garminLogin.password : v.code !== garminLogin.code;
		if (fail === 422 || wrong) return problem(r, 422, 'validation_failed', 'the provider did not accept the sign-in details or the code; begin again');
		if (step === 'login') return json(r, 200, this.prompt('code', p.name));
		return json(r, 200, { connection_id: this.connect(p).id });
	}

	private connect(p: Provider) {
		const c = {
			id: `conn_${String(this.next++).padStart(32, '0')}`, provider: p.code, mode: p.remote ? 'remote' : 'in_process',
			status: p.official ? 'active' : 'paused', official: p.official, upstream: p.upstream ?? null, health: p.official ? 'ok' : 'paused', health_reason: null,
			last_success_at: null, last_error_class: null, consecutive_failures: 0, created_at: t0, updated_at: t0
		};
		this.connections.push(c);
		Object.assign(p, { connections: p.connections + 1, setup_state: 'connected' });
		return c;
	}

	private addSidecar(r: Route, body: { name: string; url: string }) {
		if (!/^https?:\/\/(localhost|[a-z0-9-]+|10\.\d+\.\d+\.\d+)(:\d+)?\/?$/.test(body.url)) {
			return problem(r, 422, 'validation_failed', 'invalid sidecar', [{ pointer: '/url', detail: 'must resolve to a loopback, private or link-local address' }]);
		}
		const sidecar = { name: body.name, url: body.url, source: 'panel', bundled: false, available: false, created_at: new Date().toISOString() };
		this.sidecars.push(sidecar);
		return json(r, 201, { sidecar, secret: sidecarSecret });
	}

	private removeSidecar(r: Route, name: string, confirm: boolean) {
		const s = this.sidecars.find((x) => x.name === name);
		if (!s) return problem(r, 404, 'not_found', 'no such sidecar');
		if (s.source === 'environment') return problem(r, 409, 'conflict', 'set by the environment');
		if (this.connections.some((c) => c.provider === name) && !confirm) return problem(r, 409, 'conflict', 'connections of this provider exist');
		this.sidecars = this.sidecars.filter((x) => x !== s);
		return r.fulfill({ status: 204 });
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string, errors?: { pointer: string; detail: string }[]) {
	return r.fulfill({
		status, contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e', errors })
	});
}

/** `test` with the base FakeApi (signed in) plus a fresh SetupApi per test. */
export const test = base.extend<{ setup: SetupApi }>({
	setup: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const setup = new SetupApi(page);
			await setup.install();
			await use(setup);
		},
		{ auto: true }
	]
});

export { expect };
