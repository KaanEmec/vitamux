// A stateful stand-in for the connection, job and resolved endpoints used by Today and
// Connections (J11.2), on top of fake-api.ts. All values are synthetic.
//
// Seed: an unofficial "ultrahuman" connection whose stream drifted (degraded) with a backfill
// that has failed units, a Withings connection that needs reauthorization, and a healthy
// Apple Health push connection. The OAuth round trip is faked: auth/begin applies what the
// callback would do and answers a redirect straight back to /connections?connected=…. The
// sidecar provider asks for sign-in details, then a code (J17.2); its connection starts paused.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;
interface Conn {
	id: string;
	provider: string;
	mode: 'in_process' | 'push' | 'remote';
	status: string;
	official: boolean | null;
	upstream: { package: string; version: string; source_url: string } | null;
	health: string;
	health_reason: string | null;
	last_success_at: string | null;
	last_error_class: string | null;
	consecutive_failures: number;
	created_at: string;
	updated_at: string;
}
interface Backfill {
	id: string;
	connection_id: string;
	stream: string;
	start: string;
	end: string;
	status: string;
	created_at: string;
	finished_at: string | null;
	units: { start: string; end: string; status: string; attempts: number; error_class: string | null; updated_at: string }[];
}

const t0 = '2026-09-01T08:00:00Z';
const hour = 3_600_000;
const day = 24 * hour;
export const ids = {
	ultrahuman: 'conn_' + 'a'.repeat(32),
	withings: 'conn_' + 'b'.repeat(32),
	apple: 'conn_' + 'c'.repeat(32)
};
export const sidecarSecret = { username: 'synthetic-user', password: 'synthetic-pass', code: '123456' };
const upstream = { package: 'example-collector', version: '1.4.2', source_url: 'https://example.com/example-collector' };
const providers = [
	{ code: 'withings', name: 'Withings', official: true, auth_kind: 'oauth2', remote: false, available: true },
	{ code: 'example_sidecar', name: 'Example Collector', official: false, auth_kind: 'interactive_mfa', remote: true, available: true, upstream },
	{ code: 'offline_sidecar', name: 'Offline sidecar', official: false, auth_kind: null, remote: true, available: false }
];
const streamsOf: Record<string, string[]> = { example_sidecar: ['example_sidecar.heart_rate'], ultrahuman: ['ultrahuman.metrics'], withings: ['withings.measures'], apple_health: ['healthkit.samples.v1'] };

export function conn(id: string, provider: string, over: Partial<Conn>): Conn {
	return {
		id, provider, mode: 'in_process', status: 'active', official: true, upstream: null, health: 'ok', health_reason: null,
		last_success_at: new Date(Date.now() - hour).toISOString(), last_error_class: null, consecutive_failures: 0,
		created_at: t0, updated_at: t0, ...over
	};
}

function units(start: number, end: number, status: (i: number) => string) {
	const out: Backfill['units'] = [];
	for (let s = start, i = 0; s < end; s += 30 * day, i++) {
		const st = status(i);
		out.push({ start: new Date(s).toISOString(), end: new Date(Math.min(s + 30 * day, end)).toISOString(), status: st, attempts: st === 'pending' ? 0 : st === 'failed' ? 5 : 1, error_class: st === 'failed' ? 'upstream_5xx' : null, updated_at: t0 });
	}
	return out;
}

export class ConnectionsApi {
	connections: Conn[] = [
		conn(ids.ultrahuman, 'ultrahuman', {
			official: false, status: 'degraded', health: 'degraded', health_reason: 'ultrahuman.metrics: response shape changed (schema_drift)',
			last_error_class: 'schema_drift', consecutive_failures: 2
		}),
		conn(ids.withings, 'withings', {
			status: 'needs_reauth', health: 'needs_reauth', health_reason: 'refresh token rejected', last_error_class: 'auth_expired', consecutive_failures: 1,
			last_success_at: new Date(Date.now() - 3 * day).toISOString()
		}),
		conn(ids.apple, 'apple_health', { mode: 'push', official: null })
	];
	backfills: Backfill[] = [
		{
			id: '44444444-4444-4444-8444-444444444441', connection_id: ids.ultrahuman, stream: 'ultrahuman.metrics',
			start: '2026-05-01T00:00:00Z', end: '2026-07-30T00:00:00Z', status: 'failed', created_at: t0, finished_at: t0,
			units: units(Date.parse('2026-05-01T00:00:00Z'), Date.parse('2026-07-30T00:00:00Z'), (i) => (i === 1 ? 'failed' : 'done'))
		}
	];
	runs: Record<string, Json[]> = { [ids.ultrahuman]: seedRuns() };
	/** Status of GET /resolved/daily; 503 simulates the endpoint not being ready. */
	resolvedStatus = 200;
	/** Query strings of DELETE /connections/{id}. */
	deletes: string[] = [];
	/** Bodies of POST /providers/{provider}/auth/continue. */
	continues: { state: string; values: Record<string, string> }[] = [];
	/** Prompt steps waiting for their values: state -> what they ask and the connection being reauthorized. */
	private pending = new Map<string, { step: 'login' | 'code'; connection: string | null }>();
	private next = 1;

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const url = new URL(r.request().url());
		const path = url.pathname.replace('/api/v1', '');
		const method = r.request().method();
		const q = url.searchParams;
		let m: RegExpMatchArray | null;
		if (path === '/connections' && method === 'GET') return json(r, 200, { connections: this.connections });
		if (path === '/jobs') return json(r, 200, { jobs: q.get('status') === 'dead' ? [deadJob()] : [], has_more: false });
		if (path === '/resolved/daily') return this.daily(r, q);
		if (path === '/providers' && method === 'GET') return json(r, 200, { providers });
		if ((m = path.match(/^\/providers\/([^/]+)\/auth\/continue$/)) && method === 'POST') return this.continueAuth(r, m[1]);
		if ((m = path.match(/^\/providers\/([^/]+)\/auth\/begin$/)) && method === 'POST') {
			if (providers.find((p) => p.code === m![1])?.auth_kind === 'interactive_mfa') return json(r, 200, this.prompt('login', null));
			const c = conn(`conn_${String(this.next++).padStart(32, 'd')}`, m[1], { last_success_at: null });
			this.connections.push(c);
			return json(r, 200, { redirect_url: `/connections?connected=${m[1]}` });
		}
		if ((m = path.match(/^\/schedules\/([^/]+)$/)) && method === 'PATCH') {
			const s = this.schedules().find((x) => x.id === m![1]);
			return s ? json(r, 200, { ...s, ...(r.request().postDataJSON() as Json) }) : problem(r, 404, 'not_found', 'no such schedule');
		}
		if (path === '/schedules') return json(r, 200, { schedules: this.schedules().filter((s) => !q.get('connection') || s.connection_id === q.get('connection')) });
		if (!(m = path.match(/^\/connections\/(conn_[0-9a-f]{32})(\/.*)?$/))) return r.fallback();
		const c = this.connections.find((x) => x.id === m![1]);
		if (!c) return problem(r, 404, 'not_found', 'no such connection');
		return this.connection(r, c, m[2] ?? '', method, q);
	}

	private connection(r: Route, c: Conn, sub: string, method: string, q: URLSearchParams) {
		let m: RegExpMatchArray | null;
		if (sub === '' && method === 'GET') return json(r, 200, c);
		if (sub === '' && method === 'PATCH') {
			const { status } = r.request().postDataJSON() as { status: string };
			Object.assign(c, { status, health: status === 'paused' ? 'paused' : 'ok' });
			return json(r, 200, c);
		}
		if (sub === '' && method === 'DELETE') {
			this.deletes.push(q.toString());
			if (q.get('data') === 'delete') this.connections = this.connections.filter((x) => x !== c);
			else Object.assign(c, { status: 'disabled', health: 'disabled' });
			return r.fulfill({ status: 204 });
		}
		if (sub === '/auth/begin' && method === 'POST') {
			if (c.mode === 'remote') return json(r, 200, this.prompt('login', c.id));
			Object.assign(c, { status: 'active', health: 'ok', health_reason: null, last_error_class: null, consecutive_failures: 0 });
			return json(r, 200, { redirect_url: `/connections?connected=${c.provider}` });
		}
		if (sub === '/sync' && method === 'POST') {
			if (c.status === 'paused') return problem(r, 409, 'conflict', 'the connection is paused');
			const jobs = streamsOf[c.provider].map((stream) => ({
				id: `55555555-5555-4555-8555-5555555555${String(this.next++).padStart(2, '0')}`, kind: 'sync', status: 'queued', connection_id: c.id,
				priority: 0, attempts: 0, max_attempts: 5, payload: { stream, mode: 'manual' }, run_at: t0, created_at: t0, started_at: null, finished_at: null
			}));
			(this.runs[c.id] ??= []).unshift(...jobs.map((j) => ({
				id: String(this.next++), job_id: j.id, kind: 'sync', attempt: 1, started_at: new Date().toISOString(),
				finished_at: new Date(Date.now() + 2000).toISOString(), outcome: 'succeeded', error_class: null, error_message: null, stats: { records: 12 }
			})));
			c.last_success_at = new Date().toISOString();
			return json(r, 202, { jobs });
		}
		if (sub === '/runs') return json(r, 200, { runs: this.runs[c.id] ?? [], has_more: false });
		if (sub === '/streams') {
			return json(r, 200, {
				streams: (streamsOf[c.provider] ?? []).map((name) => ({
					name, health: c.health === 'degraded' ? 'degraded' : 'ok', health_reason: null,
					status: c.health === 'degraded' ? 'degraded' : 'ok', status_reason: c.health === 'degraded' ? 'schema_drift since 2026-09-13T06:00Z' : null,
					has_cursor: c.mode === 'in_process', high_watermark: t0, updated_at: t0, schedules: this.schedules().filter((s) => s.stream === name)
				}))
			});
		}
		if (sub === '/backfills' && method === 'GET') return json(r, 200, { backfills: this.backfills.filter((b) => b.connection_id === c.id).map(summary) });
		if (sub === '/backfills' && method === 'POST') {
			const body = r.request().postDataJSON() as { stream: string; start: string; end?: string };
			if (!streamsOf[c.provider].includes(body.stream)) return problem(r, 422, 'validation_failed', 'invalid backfill', [{ pointer: '/stream', detail: 'unknown stream' }]);
			const start = Date.parse(body.start);
			const end = body.end ? Date.parse(body.end) : Date.now();
			const b: Backfill = {
				id: `44444444-4444-4444-8444-4444444444${String(this.next++).padStart(2, '0')}`, connection_id: c.id, stream: body.stream,
				start: body.start, end: new Date(end).toISOString(), status: 'running', created_at: new Date().toISOString(), finished_at: null,
				units: units(start, end, () => 'pending')
			};
			this.backfills.unshift(b);
			return json(r, 202, summary(b));
		}
		if (!(m = sub.match(/^\/backfills\/([^/]+)(\/retry|\/cancel)?$/))) return r.fallback();
		const b = this.backfills.find((x) => x.id === m![1] && x.connection_id === c.id);
		if (!b) return problem(r, 404, 'not_found', 'no such backfill');
		if (!m[2]) return json(r, 200, { ...summary(b), units: b.units });
		if (m[2] === '/cancel') {
			if (b.status !== 'running' && b.status !== 'failed') return problem(r, 409, 'conflict', 'backfill is not running');
			Object.assign(b, { status: 'cancelled', finished_at: new Date().toISOString() });
			return json(r, 200, summary(b));
		}
		const only = (r.request().postDataJSON() as { unit_start?: string } | null)?.unit_start;
		const failed = b.units.filter((u) => u.status === 'failed' && (!only || u.start === only));
		if (!failed.length) return problem(r, 409, 'conflict', 'no failed or unfinished unit to retry');
		for (const u of failed) Object.assign(u, { status: 'pending', error_class: null });
		b.status = 'running';
		return json(r, 200, summary(b));
	}

	private prompt(step: 'login' | 'code', connection: string | null) {
		const state = `state-${this.next++}`;
		this.pending.set(state, { step, connection });
		const fields =
			step === 'login'
				? [{ name: 'username', label: 'Username', kind: 'text' }, { name: 'password', label: 'Password', kind: 'password' }]
				: [{ name: 'code', label: 'Verification code', kind: 'code' }];
		return { state, prompt: { message: step === 'login' ? 'Sign in to Example Collector.' : 'Enter the code it sent you.', fields } };
	}

	private continueAuth(r: Route, provider: string) {
		const body = r.request().postDataJSON() as { state: string; values: Record<string, string> };
		this.continues.push(body);
		const p = this.pending.get(body.state);
		this.pending.delete(body.state); // a state is single use
		if (!p) return problem(r, 400, 'invalid_state', 'the authorization step expired or was already used');
		const { values } = body;
		if (p.step === 'login') {
			if (values.username !== sidecarSecret.username || values.password !== sidecarSecret.password) {
				return problem(r, 422, 'auth_rejected', 'the connector did not accept the sign-in details');
			}
			return json(r, 200, this.prompt('code', p.connection));
		}
		if (values.code !== sidecarSecret.code) return problem(r, 422, 'auth_rejected', 'the code was not accepted');
		let c = this.connections.find((x) => x.id === p.connection);
		if (!c) {
			c = conn(`conn_${String(this.next++).padStart(32, 'e')}`, provider, { mode: 'remote', official: false, upstream, status: 'paused', health: 'paused', last_success_at: null });
			this.connections.push(c);
		} else {
			Object.assign(c, { status: 'active', health: 'ok', health_reason: null, last_error_class: null, consecutive_failures: 0 });
		}
		return json(r, 200, { connection_id: c.id });
	}

	private schedules() {
		return this.connections.filter((c) => c.mode === 'in_process').flatMap((c, i) =>
			streamsOf[c.provider].flatMap((stream) => [
				{ id: `66666666-6666-4666-8666-66666666660${i}`, connection_id: c.id, stream, mode: 'incremental', interval_seconds: 3600, lookback_seconds: 0, enabled: true, next_run_at: t0 },
				{ id: `66666666-6666-4666-8666-66666666661${i}`, connection_id: c.id, stream, mode: 'correction', interval_seconds: 86_400, lookback_seconds: 7 * 86_400, enabled: true, next_run_at: t0 }
			])
		);
	}

	private daily(r: Route, q: URLSearchParams) {
		if (this.resolvedStatus !== 200) return problem(r, this.resolvedStatus, 'unavailable', 'resolved values are not ready yet');
		const [start, end] = [q.get('start_date')!, q.get('end_date')!];
		const metrics = q.getAll('metrics').flatMap((m) => m.split(',')).filter(Boolean);
		const value = (metric: string, date: string): Json => {
			if (metric === 'resting_heart_rate' && date === end) {
				return {
					status: 'fallback', value: 52, unit: 'bpm', rule: { ref: 'builtin:resting_heart_rate', version: 1 },
					inputs: [
						{ group: 'ring', status: 'no_data', reason: 'stream degraded' },
						{ group: 'watch', status: 'used', selected: true, value: 52, sources: [{ provider: 'apple_health', connection_id: ids.apple }] }
					],
					explanation: 'ring had no value (stream degraded). Fell back to watch: 52 bpm.'
				};
			}
			if (metric === 'steps' && date === start) {
				return { status: 'direct', value: 9500, unit: 'count', inputs: [{ group: 'apple_health', status: 'used', selected: true, value: 9500 }], explanation: 'apple_health 9,500.' };
			}
			return { status: 'no_data', explanation: 'No source had a value.' };
		};
		const days = [start, end].map((d) => ({ local_date: d, metrics: Object.fromEntries(metrics.map((m) => [m, value(m, d)])) }));
		return json(r, 200, { timezone: 'Europe/Amsterdam', days });
	}
}

/** Hourly-ish syncs on each of the last 14 local days (noon), one of which failed (the day before yesterday). */
function seedRuns(): Json[] {
	return Array.from({ length: 14 }, (_, k) => {
		const at = new Date();
		at.setDate(at.getDate() - k);
		at.setHours(12, 0, 0, 0);
		const failed = k === 2;
		return {
			id: String(1000 - k), job_id: `55555555-5555-4555-8555-5555555550${String(k).padStart(2, '0')}`, kind: 'sync', attempt: 1,
			started_at: at.toISOString(), finished_at: new Date(at.getTime() + 2000).toISOString(), outcome: failed ? 'failed' : 'succeeded',
			error_class: failed ? 'schema_drift' : null, error_message: failed ? 'response shape changed' : null, stats: {}
		};
	});
}

function summary(b: Backfill) {
	const count = (s: string) => b.units.filter((u) => u.status === s).length;
	const { units: _units, ...rest } = b;
	void _units;
	return { ...rest, unit_counts: { pending: count('pending'), running: count('running'), done: count('done'), failed: count('failed') } };
}

function deadJob() {
	const at = new Date(Date.now() - 2 * hour).toISOString();
	return {
		id: '77777777-7777-4777-8777-777777777771', kind: 'normalize_batch', status: 'dead', connection_id: ids.ultrahuman, priority: 0,
		attempts: 5, max_attempts: 5, payload: {}, run_at: at, created_at: at, started_at: at, finished_at: at
	};
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

/** `test` with the base FakeApi (signed in) plus a fresh ConnectionsApi per test. */
export const test = base.extend<{ conns: ConnectionsApi }>({
	conns: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const conns = new ConnectionsApi(page);
			await conns.install();
			await use(conns);
		},
		{ auto: true }
	]
});

export { expect };
