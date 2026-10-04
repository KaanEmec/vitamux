// A stateful stand-in for the device, pairing, origin and source-device endpoints used by
// Settings › Devices (J15.6) and the rule builder's chips, on top of fake-api.ts. It mirrors
// internal/api/devices.go and origins.go. All values are synthetic.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;

const heart = 'HKQuantityTypeIdentifierHeartRate';
const steps = 'HKQuantityTypeIdentifierStepCount';
const sleep = 'HKCategoryTypeIdentifierSleepAnalysis';

export class DevicesApi {
	/** 'ok' answers pairing codes; a number answers that status (503 without a public URL). */
	pairing: 'ok' | number = 'ok';
	/** Seconds a pairing code lives; short values let a test watch it expire. */
	codeSeconds = 600;
	codes = 0;
	devices: Json[] = [
		{
			id: '00000000-0000-4000-8000-0000000000d1', name: 'Synthetic iPhone', connection_id: 'conn_00000000000000000000000000000001',
			created_at: '2026-09-01T08:00:00Z', last_seen_at: new Date(Date.now() - 3600_000).toISOString(), last_sync_at: new Date(Date.now() - 7200_000).toISOString(),
			revoked_at: null, types: [heart, steps, sleep], possibly_denied: [sleep], anchor_resets: []
		},
		{
			id: '00000000-0000-4000-8000-0000000000d2', name: 'Old synthetic iPad', connection_id: 'conn_00000000000000000000000000000001',
			created_at: '2026-06-01T08:00:00Z', last_seen_at: '2026-06-02T08:00:00Z', last_sync_at: null, revoked_at: '2026-06-03T08:00:00Z',
			types: [], possibly_denied: [], anchor_resets: []
		}
	];
	origins: Json[] = [
		{ id: '00000000-0000-4000-8000-0000000000e1', provider: 'apple_health', origin_key: 'com.example.synthetic.garmin', name: 'Synthetic Garmin app', is_native: false, relayed_provider: null, created_at: '2026-09-02T08:00:00Z' },
		{ id: '00000000-0000-4000-8000-0000000000e2', provider: 'apple_health', origin_key: 'com.apple.health.synthetic', name: null, is_native: true, relayed_provider: null, created_at: '2026-09-02T08:00:00Z' }
	];
	sourceDevices: Json[] = [
		{ id: 'dev_00000000000000000000000000000001', provider: 'apple_health', device_type: 'watch', manufacturer: 'Synthetic', model: 'Watch 1' }
	];
	/** Bodies of request-anchor-reset (by device id) and of PATCH /origins/{id}, in order. */
	resets: { id: string; body: Json }[] = [];
	classified: { id: string; body: Json }[] = [];
	revoked: string[] = [];

	constructor(private page: Page) {}

	async install() {
		await this.page.route('**/api/v1/**', (r) => this.dispatch(r));
	}

	private dispatch(r: Route) {
		const path = new URL(r.request().url()).pathname.replace('/api/v1', '');
		const method = r.request().method();
		const body = () => (r.request().postData() ? (r.request().postDataJSON() as Json) : {});
		let m: RegExpMatchArray | null;

		if (path === '/devices' && method === 'GET') return json(r, 200, { devices: this.devices });
		if (path === '/devices/pairing-codes' && method === 'POST') {
			if (this.pairing !== 'ok') return problem(r, this.pairing, 'unavailable', 'pairing needs VITAMUX_PUBLIC_URL, the address devices reach Vitamux at');
			this.codes++;
			const code = `SYNT-${String(this.codes).padStart(4, '0')}`;
			return json(r, 201, {
				code, expires_at: new Date(Date.now() + this.codeSeconds * 1000).toISOString(), url: 'https://vitamux.example.test',
				qr_payload: JSON.stringify({ url: 'https://vitamux.example.test', code })
			});
		}
		if ((m = path.match(/^\/devices\/([^/]+)\/request-anchor-reset$/)) && method === 'POST') {
			this.resets.push({ id: m[1], body: body() });
			const d = this.devices.find((x) => x.id === m![1]);
			if (!d) return problem(r, 404, 'not_found', 'no such active device');
			const types = (body().types as string[] | undefined) ?? ['*'];
			(d.anchor_resets as Json[]).push(...types.map((type) => ({ type, requested_at: '2026-10-04T09:00:00Z' })));
			return r.fulfill({ status: 204 });
		}
		if ((m = path.match(/^\/devices\/([^/]+)\/revoke$/)) && method === 'POST') {
			const d = this.devices.find((x) => x.id === m![1]);
			if (!d) return problem(r, 404, 'not_found', 'no such active device');
			d.revoked_at = '2026-10-04T09:00:00Z';
			this.revoked.push(m[1]);
			return r.fulfill({ status: 204 });
		}
		if (path === '/origins' && method === 'GET') {
			return json(r, 200, { origins: this.origins, relay_targets: [{ code: 'garmin', name: 'Garmin' }, { code: 'oura', name: 'Oura' }] });
		}
		if ((m = path.match(/^\/origins\/([^/]+)$/)) && method === 'PATCH') {
			const o = this.origins.find((x) => x.id === m![1]);
			if (!o) return problem(r, 404, 'not_found', 'no such origin');
			this.classified.push({ id: m[1], body: body() });
			o.relayed_provider = body().relayed_provider;
			return r.fulfill({ status: 204 });
		}
		if (path === '/source-devices' && method === 'GET') return json(r, 200, { devices: this.sourceDevices });
		return r.fallback();
	}
}

function json(r: Route, status: number, body: unknown) {
	return r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
}
function problem(r: Route, status: number, code: string, detail: string) {
	return r.fulfill({
		status, contentType: 'application/problem+json',
		body: JSON.stringify({ type: 'about:blank', title: code, status, code, detail, request_id: 'req-e2e' })
	});
}

/** `test` with the base FakeApi (signed in) plus a fresh DevicesApi per test. */
export const test = base.extend<{ devices: DevicesApi }>({
	devices: [
		async ({ page, api }, use) => {
			api.signedIn = true;
			const devices = new DevicesApi(page);
			await devices.install();
			await use(devices);
		},
		{ auto: true }
	]
});

export { expect };
