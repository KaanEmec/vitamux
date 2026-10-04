// A stateful stand-in for the settings, timezone, API key, export, TOTP, password, session and status
// endpoints used by the Settings section (J11.5), on top of fake-api.ts. All values are
// synthetic. Settings PATCH is a merge patch (retention.raw_days merges per provider).
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
interface Period { id: string; tz: string; valid_from: string; valid_to: string | null }
interface Key { id: string; name: string; scopes: string[]; created_at: string; last_used_at: string | null; expires_at: string | null; revoked_at: string | null }

export class SettingsApi {
	periods: Period[] = [{ id: '00000000-0000-4000-8000-0000000000a1', tz: 'Europe/London', valid_from: '2020-01-01T00:00:00Z', valid_to: null }];
	keys: Key[] = [];
	/** Bodies of POST /timezone-periods and POST /api-keys, in order. */
	periodBodies: Json[] = [];
	keyBodies: Json[] = [];
	settings: Json = {
		'withings.notifications': false,
		'documents.external_ai.gemini.enabled': false,
		'documents.external_ai.openai.enabled': false,
		'documents.retention_days': null,
		'retention.raw_days': { withings: 90 },
		'retention.superseded_after_days': 0,
		'retention.idempotency_key_days': 30,
		'retention.future_flag': false,
		'sources.priority': ['whoop']
	};
	/** Bodies of PATCH /settings, in order. */
	patches: Json[] = [];
	/** Body of the last POST /exports and the number of GET /exports/{id} polls. */
	exportBody: Json | null = null;
	exportPolls = 0;
	/** Status for GET /system/status: the object to return, or null for 404. */
	status: Json | null = {
		last_backup: '2026-10-02T03:00:00Z',
		database_size_bytes: 52_428_800,
		blob_size_bytes: 1_048_576,
		degraded_connections: [{ id: 'conn_00000000000000000000000000000001', name: 'whoop', error_class: 'schema_drift' }],
		failing_jobs: []
	};
	totpEnabled = false;
	/** Bodies of POST /auth/password, in order. */
	passwordBodies: Json[] = [];
	sessions = [
		{ id: '00000000-0000-4000-8000-0000000000c1', created_at: '2026-10-03T08:00:00Z', last_seen_at: '2026-10-03T09:00:00Z', expires_at: '2026-10-10T08:00:00Z', current: true },
		{ id: '00000000-0000-4000-8000-0000000000c2', created_at: '2026-10-01T08:00:00Z', last_seen_at: '2026-10-02T09:00:00Z', expires_at: '2026-10-08T08:00:00Z', current: false },
		{ id: '00000000-0000-4000-8000-0000000000c3', created_at: '2026-09-30T08:00:00Z', last_seen_at: '2026-10-01T09:00:00Z', expires_at: '2026-10-07T08:00:00Z', current: false }
	];
	private next = 1;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const path = new URL(r.request().url()).pathname.replace('/api/v1', '');
		const method = r.request().method();
		const body = () => r.request().postDataJSON() as Json;
		let m: RegExpMatchArray | null;

		if (path === '/timezone-periods' && method === 'GET') return json(r, 200, { timezone_periods: this.periods });
		if (path === '/timezone-periods' && method === 'POST') {
			const b = body();
			this.periodBodies.push(b);
			const p: Period = { id: `00000000-0000-4000-8000-${String(this.next++).padStart(12, '0')}`, tz: b.tz as string, valid_from: b.valid_from as string, valid_to: null };
			this.periods[this.periods.length - 1].valid_to = p.valid_from;
			this.periods.push(p);
			return json(r, 201, p);
		}
		if ((m = path.match(/^\/timezone-periods\/([^/]+)$/)) && method === 'DELETE') {
			const i = this.periods.findIndex((p) => p.id === m![1]);
			if (i < 0) return problem(r, 404, 'not_found', 'no such period');
			this.periods.splice(i, 1);
			if (this.periods.length) this.periods[this.periods.length - 1].valid_to = null;
			return r.fulfill({ status: 204 });
		}

		if (path === '/api-keys' && method === 'GET') return json(r, 200, { api_keys: this.keys });
		if (path === '/api-keys' && method === 'POST') {
			const b = body();
			this.keyBodies.push(b);
			const k: Key = { id: `00000000-0000-4000-8000-${String(this.next++).padStart(12, '0')}`, name: b.name as string, scopes: b.scopes as string[], created_at: '2026-10-03T09:00:00Z', last_used_at: null, expires_at: (b.expires_at as string) ?? null, revoked_at: null };
			this.keys.push(k);
			return json(r, 201, { ...k, token: 'vmx_synthetic_secret_once' });
		}
		if ((m = path.match(/^\/api-keys\/([^/]+)$/)) && method === 'DELETE') {
			const k = this.keys.find((x) => x.id === m![1]);
			if (!k) return problem(r, 404, 'not_found', 'no such key');
			k.revoked_at = '2026-10-03T09:05:00Z';
			return r.fulfill({ status: 204 });
		}

		if (path === '/settings' && method === 'GET') return json(r, 200, this.settings);
		if (path === '/settings' && method === 'PATCH') {
			const b = body();
			this.patches.push(b);
			if (b['retention.idempotency_key_days'] !== undefined && (b['retention.idempotency_key_days'] as number) < 7) {
				return problem(r, 422, 'validation_failed', 'invalid settings', [{ pointer: '/retention.idempotency_key_days', detail: 'must be at least 7' }]);
			}
			for (const [k, v] of Object.entries(b)) {
				this.settings[k] = k === 'retention.raw_days' ? { ...(this.settings[k] as Json), ...(v as Json) } : v;
			}
			return json(r, 200, this.settings);
		}

		if (path === '/exports' && method === 'POST') {
			this.exportBody = body();
			this.exportPolls = 0;
			return json(r, 202, { id: 'exp1', status: 'queued', format: this.exportBody.format, include_raw: !!this.exportBody.include_raw, created_at: '2026-10-03T09:10:00Z' });
		}
		if (path === '/exports/exp1' && method === 'GET') {
			this.exportPolls++;
			if (this.exportPolls < 2) return json(r, 200, { id: 'exp1', status: 'running', created_at: '2026-10-03T09:10:00Z' });
			return json(r, 200, { id: 'exp1', status: 'done', format: 'ndjson', include_raw: false, created_at: '2026-10-03T09:10:00Z', finished_at: '2026-10-03T09:10:05Z', size_bytes: 2_097_152, download_url: '/api/v1/exports/exp1/download?token=one-time' });
		}
		if (path === '/exports/exp1/download') return r.fulfill({ status: 200, contentType: 'application/zip', body: 'PK' });

		if (path === '/auth/totp/enroll') return json(r, 200, { secret: 'SYNTHETICSECRET', otpauth_uri: 'otpauth://totp/Vitamux:owner?secret=SYNTHETICSECRET' });
		if (path === '/auth/totp/confirm') {
			if ((body().code as string) !== '123456') return problem(r, 422, 'validation_failed', 'invalid code', [{ pointer: '/code', detail: 'code does not match' }]);
			this.totpEnabled = true;
			return json(r, 200, { recovery_codes: ['synthetic-recovery-a', 'synthetic-recovery-b'] });
		}
		if (path === '/auth/totp/disable') {
			if ((body().password as string) !== 'synthetic-password') return problem(r, 401, 'unauthenticated', 'invalid password or code');
			this.totpEnabled = false;
			return r.fulfill({ status: 204 });
		}
		if (path === '/auth/password' && method === 'POST') {
			const b = body();
			this.passwordBodies.push(b);
			if (b.current_password !== 'synthetic-password') return problem(r, 422, 'validation_failed', 'the current password is wrong', [{ pointer: '/current_password', detail: 'does not match' }]);
			this.sessions = this.sessions.filter((s) => s.current);
			return r.fulfill({ status: 204 });
		}
		if (path === '/auth/sessions' && method === 'GET') return json(r, 200, { sessions: this.sessions });
		if ((m = path.match(/^\/auth\/sessions\/([^/]+)$/)) && method === 'DELETE') {
			if (!this.sessions.some((s) => s.id === m![1])) return problem(r, 404, 'not_found', 'no session with this id');
			this.sessions = this.sessions.filter((s) => s.id !== m![1]);
			return r.fulfill({ status: 204 });
		}
		if (path === '/auth/session' && method === 'GET') {
			return json(r, 200, { user: { id: '00000000-0000-4000-8000-000000000001', username: 'owner', totp_enabled: this.totpEnabled }, csrf_token: 'synthetic-csrf' });
		}

		if (path === '/system/status') return this.status ? json(r, 200, this.status) : problem(r, 404, 'not_found', 'not available');
		return r.fallback();
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

/** `test` with the base FakeApi (signed in) plus a fresh SettingsApi per test. */
export const test = base.extend<{ settings: SettingsApi }>({
	settings: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const settings = new SettingsApi(page);
			await settings.install();
			await use(settings);
		},
		{ auto: true }
	]
});

export { expect };
