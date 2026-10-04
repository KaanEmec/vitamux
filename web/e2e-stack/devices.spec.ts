// Real-stack device flow (J15.6): pair a fake device through the ingest endpoint with a code from
// the UI, classify a seeded origin as a relay, and build "Apple Watch heart rate first, exclude
// relayed" with the builder's chips. Seeds: scripts/e2e-stack.sh (synthetic origins and a watch).
// Run via `make test-e2e-stack`; skipped without the stack variables.
import { expect, test } from '@playwright/test';

const username = process.env.VITAMUX_E2E_USER;
const password = process.env.VITAMUX_E2E_PASSWORD;
test.skip(!username || !password, 'run via scripts/e2e-stack.sh');

test('pair, classify a relay origin, build Apple Watch first without relayed data', async ({ page }) => {
	const errors: string[] = [];
	page.on('pageerror', (e) => errors.push(e.message));

	await page.goto('/login');
	await page.getByLabel('Username').fill(username!);
	await page.getByLabel('Password').fill(password!);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');

	// Pair: the code from the UI is redeemed by a fake device on the ingest endpoint.
	await page.goto('/settings/devices');
	await expect(page.getByText('No devices paired yet.')).toBeVisible();
	await page.getByRole('button', { name: 'Create pairing code' }).click();
	const code = (await page.getByLabel('Pairing code').textContent())!.trim();
	await expect(page.getByRole('img', { name: /Pairing QR code/ })).toBeVisible();
	const paired = await page.request.post('/api/ingest/v1/devices/pair', { data: { code, name: 'Synthetic iPhone' } });
	expect(paired.status()).toBe(201);
	await page.getByRole('button', { name: 'Refresh' }).click();
	const device = page.getByRole('row', { name: /Synthetic iPhone/ });
	await expect(device).toBeVisible();
	await expect(device.getByText('Not reported yet')).toBeVisible();

	// Classify the seeded Garmin app as a relay; a native origin cannot be changed.
	const garmin = page.getByRole('row', { name: /Synthetic Garmin app/ });
	await expect(garmin).toContainText('Direct');
	await garmin.getByLabel('Vendor relayed by Synthetic Garmin app').selectOption({ label: 'Garmin' });
	await expect(garmin).toContainText('Relayed from Garmin');
	await page.reload();
	await expect(page.getByRole('row', { name: /Synthetic Garmin app/ })).toContainText('Relayed from Garmin');
	await expect(page.getByRole('row', { name: /Synthetic Apple origin/ }).getByRole('combobox')).toHaveCount(0);

	// Build the rule from chips: watch (direct) first, other direct Apple Health data second, relayed excluded.
	await page.goto('/rules/new?metric=heart_rate&blank=1');
	await page.getByRole('button', { name: /Sources/ }).click();
	await page.getByLabel('Group id').fill('apple_watch');
	const match = page.getByRole('group', { name: 'Add a match from your sources' });
	await match.getByRole('button', { name: '+ provider apple_health, device type watch, not relayed' }).click();
	await page.getByRole('button', { name: 'Remove match 1' }).click(); // the empty starter selector
	await page.getByRole('button', { name: 'Add group' }).click();
	await page.getByLabel('Group id').last().fill('apple_direct');
	await match.last().getByRole('button', { name: '+ provider apple_health, not relayed', exact: true }).click();
	await page.getByRole('button', { name: 'Remove match 1' }).last().click();
	await page.getByRole('group', { name: 'Add a exclusion from your sources' }).getByRole('button', { name: '+ provider apple_health, relayed', exact: true }).click();

	await page.getByRole('button', { name: /Window and quality/ }).click();
	await page.getByLabel('Window', { exact: true }).selectOption('bucket');
	await page.getByRole('button', { name: /Review/ }).click();
	await page.getByRole('button', { name: 'Save version' }).click();
	await expect(page).toHaveURL(/\/rules\/heart_rate\?saved=/);

	const res = await page.request.get('/api/v1/rules/heart_rate/versions');
	const { versions } = (await res.json()) as { versions: { active: boolean; spec: Record<string, unknown> }[] };
	const spec = versions.find((v) => v.active)!.spec;
	expect(spec.groups).toEqual([
		{ id: 'apple_watch', match: [{ provider: 'apple_health', device_type: 'watch', relayed: false }] },
		{ id: 'apple_direct', match: [{ provider: 'apple_health', relayed: false }] }
	]);
	expect(spec.exclude).toEqual([{ provider: 'apple_health', relayed: true }]);

	// The coverage filter lists the origins.
	await expect(page.getByLabel('Origin app')).toContainText('Synthetic Garmin app');
	expect(errors, 'page errors').toEqual([]);
});
