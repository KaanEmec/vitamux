import { expect, test } from './devices-fake';

test('pair: the QR code and text code show with a countdown, then expire', async ({ page, devices }) => {
	devices.codeSeconds = 3;
	await page.goto('/settings/devices');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Settings');
	await page.getByRole('button', { name: 'Create pairing code' }).click();

	await expect(page.getByLabel('Pairing code')).toHaveText('SYNT-0001');
	const qr = page.getByRole('img', { name: /Pairing QR code/ });
	await expect(qr).toBeVisible();
	await expect(qr.locator('path')).toHaveAttribute('d', /^M\d+ \d+h1v1h-1z/);
	await expect(page.getByRole('timer')).toHaveText(/^0:0[0-3]$/);
	await expect(page.getByText('The pairing code expired.')).toBeVisible({ timeout: 8000 });
	await expect(page.getByLabel('Pairing code')).toHaveCount(0);
	await page.getByRole('button', { name: 'Create pairing code' }).click();
	await expect(page.getByLabel('Pairing code')).toHaveText('SYNT-0002');
});

test('pairing without a public URL shows the server problem', async ({ page, devices }) => {
	devices.pairing = 503;
	await page.goto('/settings/devices');
	await page.getByRole('button', { name: 'Create pairing code' }).click();
	await expect(page.getByRole('alert')).toContainText('VITAMUX_PUBLIC_URL');
});

test('device list: types with a possibly-denied flag, resync some types, revoke with confirm', async ({ page, devices }) => {
	await page.goto('/settings/devices');
	const row = page.getByRole('row', { name: /Synthetic iPhone/ });
	await expect(row.getByText('Heart rate')).toBeVisible();
	await expect(row.getByText('Sleep analysis', { exact: false })).toContainText('possibly denied');
	await expect(row.getByText('possibly denied')).toHaveCount(1);
	await expect(page.getByRole('row', { name: /Old synthetic iPad/ })).toContainText('Revoked');
	await expect(page.getByRole('row', { name: /Old synthetic iPad/ }).getByRole('button')).toHaveCount(0);

	await row.getByRole('button', { name: 'Resync Synthetic iPhone' }).click();
	const dialog = page.getByRole('dialog', { name: 'Resync Synthetic iPhone' });
	await dialog.getByLabel('Only some types').check();
	await expect(dialog.getByRole('button', { name: 'Request resync' })).toBeDisabled();
	await dialog.getByLabel('Heart rate').check();
	await dialog.getByRole('button', { name: 'Request resync' }).click();
	expect(devices.resets).toEqual([{ id: '00000000-0000-4000-8000-0000000000d1', body: { types: ['HKQuantityTypeIdentifierHeartRate'] } }]);
	await expect(page.getByText('Resync requested for Heart rate.')).toBeVisible();

	await row.getByRole('button', { name: 'Resync Synthetic iPhone' }).click();
	await page.getByRole('dialog').getByRole('button', { name: 'Request resync' }).click();
	expect(devices.resets[1].body).toEqual({});

	await row.getByRole('button', { name: 'Revoke Synthetic iPhone' }).click();
	expect(devices.revoked).toEqual([]);
	await row.getByRole('button', { name: 'Confirm revoke' }).click();
	await expect(page.getByText('Revoked Synthetic iPhone.')).toBeVisible();
	await expect(row.getByRole('button', { name: /Revoke/ })).toHaveCount(0);
});

test('origins: classify an app as relaying a vendor, then clear it; native origins are fixed', async ({ page, devices }) => {
	await page.goto('/settings/devices');
	const garmin = page.getByRole('row', { name: /Synthetic Garmin app/ });
	await expect(garmin).toContainText('Direct');
	await garmin.getByLabel('Vendor relayed by Synthetic Garmin app').selectOption('garmin');
	await expect(page.getByRole('status').filter({ hasText: 'now relays Garmin' })).toBeVisible();
	await expect(garmin).toContainText('Relayed from Garmin');
	expect(devices.classified[0].body).toEqual({ relayed_provider: 'garmin' });

	await garmin.getByLabel('Vendor relayed by Synthetic Garmin app').selectOption('');
	await expect(garmin).toContainText('Direct');
	expect(devices.classified[1].body).toEqual({ relayed_provider: null });

	const native = page.getByRole('row', { name: /com\.apple\.health\.synthetic/ });
	await expect(native).toContainText('Native');
	await expect(native.getByRole('combobox')).toHaveCount(0);
});
