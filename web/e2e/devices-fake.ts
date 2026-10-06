// A stateful stand-in for the device, pairing, origin, source-filter and source-device endpoints
// used by Settings › Devices (J15.6, J22.25) and the rule builder's chips, on top of fake-api.ts.
// It mirrors internal/api/devices.go and origins.go. All values are synthetic.
import type { Page, Route } from '@playwright/test';
import { test as base, expect } from './fake-api';

type Json = Record<string, unknown>;

const heart = 'HKQuantityTypeIdentifierHeartRate';
const steps = 'HKQuantityTypeIdentifierStepCount';
const sleep = 'HKCategoryTypeIdentifierSleepAnalysis';
const bodyMass = 'HKQuantityTypeIdentifierBodyMass';
const hoursAgo = (h: number) => new Date(Date.now() - h * 3600_000).toISOString();

/** One app the phone found in Apple Health, with the server's default for it. */
interface App {
	bundle_id: string;
	name: string | null;
	default_mode: 'take' | 'ignore';
	default_reason: 'native' | 'direct_connection' | null;
	reason_provider: string | null;
	reason_provider_name: string | null;
	origin_id: string | null;
	classification: 'native' | 'relayed' | 'direct';
	relayed_provider: string | null;
	writes: Json[];
	ignored_records: number;
}

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
	// What GET /source-devices lists (J20.7): Apple's own devices, a Garmin watch relayed through Apple
	// Health and the same watch direct (merged into it), a device the owner named, and a merged stand-in.
	sourceDevices: Json[] = [
		sourceDevice('1', 'apple_health', 'Apple Inc.', 'Watch', { device_type: 'watch' }),
		sourceDevice('2', 'apple_health', 'Apple Inc.', 'iPhone', { device_type: 'phone' }),
		sourceDevice('3', 'apple_health', 'Garmin', 'Forerunner 965', { device_type: 'watch' }),
		sourceDevice('4', 'garmin', 'Garmin', 'Forerunner 965', { device_type: 'watch', name: 'My Forerunner' }),
		sourceDevice('5', 'garmin', 'Garmin', 'Wearable stand-in', { merged_into: 'dev_00000000000000000000000000000004' })
	];
	/** GET /providers: names for the codes above (setup details are not used by the rule builder). */
	providers: Json[] = [
		{ code: 'apple_health', name: 'Apple Health', official: true, auth_kind: 'device_pairing', remote: false, available: true, setup_state: 'connected', callback_url: null, problems: [], app_credentials: null, sidecar: null, connections: 1 },
		{ code: 'garmin', name: 'Garmin Connect', official: false, auth_kind: 'interactive_mfa', remote: true, available: true, setup_state: 'connected', callback_url: null, problems: [], app_credentials: null, sidecar: null, connections: 1 }
	];
	/** The first device's Apple Health source filter: the apps it found and the owner's explicit choices. */
	filterDevice = '00000000-0000-4000-8000-0000000000d1';
	filterVersion = 3;
	sourcesReportedAt: string | null = hoursAgo(2);
	apps: App[] = [
		{
			bundle_id: 'com.apple.health.synthetic', name: 'Synthetic Watch', default_mode: 'take', default_reason: 'native', reason_provider: null, reason_provider_name: null,
			origin_id: '00000000-0000-4000-8000-0000000000e2', classification: 'native', relayed_provider: null,
			writes: [{ type: heart, last_sample_at: hoursAgo(1) }, { type: steps, last_sample_at: hoursAgo(3) }], ignored_records: 0
		},
		{
			bundle_id: 'com.example.synthetic.band', name: 'Synthetic Band', default_mode: 'ignore', default_reason: 'direct_connection', reason_provider: 'synthetic_band',
			reason_provider_name: 'Synthetic Band Cloud', origin_id: '00000000-0000-4000-8000-0000000000e3', classification: 'relayed', relayed_provider: 'synthetic_band',
			writes: [{ type: heart, last_sample_at: hoursAgo(5) }, { type: sleep, last_sample_at: hoursAgo(9) }], ignored_records: 42
		},
		{
			bundle_id: 'com.example.synthetic.scale', name: 'Synthetic Scale', default_mode: 'take', default_reason: null, reason_provider: null, reason_provider_name: null,
			origin_id: null, classification: 'direct', relayed_provider: null, writes: [{ type: bodyMass, last_sample_at: hoursAgo(30) }], ignored_records: 0
		}
	];
	/** The explicit choices (SourceFilterChoice), as the last PUT left them. */
	choices: Json[] = [];
	/** Bodies of PUT /devices/{id}/source-filter, in order (also the refused ones). */
	filterPuts: Json[] = [];

	/** Another client changes the filter: the version moves on. */
	changeElsewhere() {
		this.filterVersion++;
	}

	private filterView(): Json {
		return {
			device_id: this.filterDevice, version: this.filterVersion, sources_reported_at: this.sourcesReportedAt,
			default_ignore: [{ origin_pattern: 'com.example.synthetic.band%', provider: 'synthetic_band', provider_name: 'Synthetic Band Cloud' }],
			origins: this.apps.map((a) => {
				const c = this.choices.find((x) => x.bundle_id === a.bundle_id);
				const mode = (c?.mode as string | undefined) ?? a.default_mode;
				return { ...a, mode, types: mode === 'per_type' ? c!.types : [], explicit: !!c };
			})
		};
	}

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
		if ((m = path.match(/^\/devices\/([^/]+)\/source-filter$/))) {
			const d = this.devices.find((x) => x.id === m![1]);
			if (!d || d.revoked_at || d.id !== this.filterDevice) return problem(r, 404, 'not_found', 'no such active device');
			if (method === 'GET') return json(r, 200, this.filterView());
			if (method !== 'PUT') return r.fallback();
			const b = body();
			this.filterPuts.push(b);
			if (b.version !== undefined && b.version !== this.filterVersion) {
				return problem(r, 409, 'conflict', 'the source filter changed since it was read; reload it');
			}
			const origins = (b.origins as Json[] | undefined) ?? [];
			for (const c of origins) {
				if (!['take', 'ignore', 'per_type'].includes(c.mode as string) || (c.mode === 'per_type' && !(c.types as string[] | undefined)?.length)) {
					return problem(r, 422, 'invalid', 'per_type needs at least one type');
				}
			}
			this.choices = origins.map((c) => (c.mode === 'per_type' ? c : { bundle_id: c.bundle_id, name: c.name, mode: c.mode }));
			this.filterVersion++;
			return json(r, 200, this.filterView());
		}
		if ((m = path.match(/^\/devices\/([^/]+)\/revoke$/)) && method === 'POST') {
			const d = this.devices.find((x) => x.id === m![1]);
			if (!d) return problem(r, 404, 'not_found', 'no such active device');
			d.revoked_at = '2026-10-04T09:00:00Z';
			this.revoked.push(m[1]);
			return r.fulfill({ status: 204 });
		}
		if (path === '/origins' && method === 'GET') {
			return json(r, 200, { origins: this.origins, relay_targets: [{ code: 'garmin', name: 'Garmin' }, { code: 'oura', name: 'Oura' }, { code: 'synthetic_band', name: 'Synthetic Band Cloud' }] });
		}
		if ((m = path.match(/^\/origins\/([^/]+)$/)) && method === 'PATCH') {
			const o = this.origins.find((x) => x.id === m![1]);
			if (!o) return problem(r, 404, 'not_found', 'no such origin');
			this.classified.push({ id: m[1], body: body() });
			o.relayed_provider = body().relayed_provider;
			return r.fulfill({ status: 204 });
		}
		if (path === '/source-devices' && method === 'GET') return json(r, 200, { devices: this.sourceDevices });
		if (path === '/providers' && method === 'GET') return json(r, 200, { providers: this.providers });
		return r.fallback();
	}
}

function sourceDevice(n: string, provider: string, manufacturer: string, model: string, over: Json): Json {
	return {
		id: `dev_${n.padStart(32, '0')}`, provider, fingerprint: `synthetic-${n}`, name: null, device_type: null, manufacturer, model, merged_into: null, ...over
	};
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
