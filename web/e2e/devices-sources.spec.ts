// Settings › Devices › Sources (J22.25): the Apple Health source filter of a paired iPhone.
import { expect, test } from './devices-fake';

const sources = '/settings/devices/00000000-0000-4000-8000-0000000000d1/sources';
const band = { bundle_id: 'com.example.synthetic.band', name: 'Synthetic Band' };
const scale = { bundle_id: 'com.example.synthetic.scale', name: 'Synthetic Scale' };
const watch = { bundle_id: 'com.apple.health.synthetic', name: 'Synthetic Watch' };

test('reached from the device row: what each app writes, its classification, its default and why', async ({ page }) => {
	await page.goto('/settings/devices');
	await expect(page.getByRole('row', { name: /Old synthetic iPad/ }).getByRole('link', { name: /Sources/ })).toHaveCount(0);
	await page.getByRole('link', { name: 'Sources of Synthetic iPhone' }).click();
	await expect(page).toHaveURL(sources);
	await expect(page.getByRole('heading', { level: 2, name: 'Sources of Synthetic iPhone' })).toBeVisible();
	await expect(page.getByRole('navigation', { name: 'Settings pages' }).getByRole('link', { name: 'Devices' })).toHaveAttribute('aria-current', 'true');
	await expect(page.getByText('Apps reported by the phone 2 h ago.')).toBeVisible();

	const watchRow = page.getByRole('article', { name: 'Synthetic Watch' });
	await expect(watchRow).toContainText('Native');
	await expect(watchRow).toContainText('com.apple.health.synthetic');
	await expect(watchRow.getByRole('list', { name: 'What Synthetic Watch writes' })).toContainText('Heart rate · 1 h ago');
	await expect(watchRow.getByRole('list', { name: 'What Synthetic Watch writes' })).toContainText('Step count · 3 h ago');
	await expect(watchRow.getByText('Default', { exact: true })).toBeVisible();
	await expect(watchRow.getByRole('group', { name: 'Synthetic Watch: take or ignore' }).getByRole('button', { name: 'Take' })).toHaveAttribute('aria-pressed', 'true');
	await expect(watchRow).not.toContainText('count twice');

	const bandRow = page.getByRole('article', { name: 'Synthetic Band' });
	await expect(bandRow).toContainText('Relayed from Synthetic Band Cloud');
	await expect(bandRow).toContainText('You get Synthetic Band Cloud directly; its copy in Apple Health would count twice.');
	await expect(bandRow).toContainText('42 records held raw, not used');
	await expect(bandRow.getByRole('group', { name: 'Synthetic Band: take or ignore' }).getByRole('button', { name: 'Ignore' })).toHaveAttribute('aria-pressed', 'true');
	await expect(bandRow.getByRole('button', { name: /Use default/ })).toHaveCount(0);
	await expect(bandRow.getByRole('link', { name: 'Show Synthetic Band in Explore' })).toHaveAttribute('href', '/explore?origin=com.example.synthetic.band');

	const scaleRow = page.getByRole('article', { name: 'Synthetic Scale' });
	await expect(scaleRow).toContainText('Direct');
	await expect(scaleRow).toContainText('Body mass');
	await expect(scaleRow).not.toContainText('records held raw');

	await page.getByRole('link', { name: '← Devices' }).click();
	await expect(page).toHaveURL('/settings/devices');
});

test('take and ignore save the full list of explicit choices with the version; Use default drops one', async ({ page, devices }) => {
	await page.goto(sources);
	const bandRow = page.getByRole('article', { name: 'Synthetic Band' });
	await bandRow.getByRole('button', { name: 'Take' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Band is now taken. The phone pulls its history on its next sync.' })).toBeVisible();
	expect(devices.filterPuts[0]).toEqual({ version: 3, origins: [{ ...band, mode: 'take' }] });
	await expect(bandRow.getByText('Your choice')).toBeVisible();
	await expect(bandRow.getByRole('button', { name: 'Take' })).toHaveAttribute('aria-pressed', 'true');
	// The reason for the default stays visible after the owner overrides it.
	await expect(bandRow).toContainText('would count twice');

	const scaleRow = page.getByRole('article', { name: 'Synthetic Scale' });
	await scaleRow.getByRole('button', { name: 'Ignore' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Scale is now ignored. The phone stops sending its data on its next sync.' })).toBeVisible();
	expect(devices.filterPuts[1]).toEqual({ version: 4, origins: [{ ...band, mode: 'take' }, { ...scale, mode: 'ignore' }] });

	await bandRow.getByRole('button', { name: 'Use default for Synthetic Band' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Band follows its default again: ignored. The phone stops sending its data' })).toBeVisible();
	expect(devices.filterPuts[2]).toEqual({ version: 5, origins: [{ ...scale, mode: 'ignore' }] });
	await expect(bandRow.getByText('Default', { exact: true })).toBeVisible();
	await expect(bandRow.getByRole('button', { name: 'Ignore' })).toHaveAttribute('aria-pressed', 'true');
});

test('Choose types: some types is per_type, all is take, none is ignore', async ({ page, devices }) => {
	await page.goto(sources);
	const watchRow = page.getByRole('article', { name: 'Synthetic Watch' });
	await watchRow.getByRole('button', { name: 'Choose types from Synthetic Watch' }).click();
	let dialog = page.getByRole('dialog', { name: 'Types from Synthetic Watch' });
	await expect(dialog.getByLabel('Heart rate')).toBeChecked();
	await expect(dialog.getByLabel('Step count')).toBeChecked();
	await dialog.getByLabel('Step count').uncheck();
	await dialog.getByRole('button', { name: 'Save types' }).click();
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Watch is now taken for Heart rate only.' })).toBeVisible();
	expect(devices.filterPuts[0]).toEqual({ version: 3, origins: [{ ...watch, mode: 'per_type', types: ['HKQuantityTypeIdentifierHeartRate'] }] });
	await expect(watchRow).toContainText('Takes only Heart rate');
	const segmented = watchRow.getByRole('group', { name: 'Synthetic Watch: take or ignore' });
	await expect(segmented.getByRole('button', { name: 'Take' })).toHaveAttribute('aria-pressed', 'false');
	await expect(segmented.getByRole('button', { name: 'Ignore' })).toHaveAttribute('aria-pressed', 'false');

	await watchRow.getByRole('button', { name: 'Choose types from Synthetic Watch' }).click();
	dialog = page.getByRole('dialog', { name: 'Types from Synthetic Watch' });
	await expect(dialog.getByLabel('Step count')).not.toBeChecked();
	await dialog.getByLabel('Step count').check();
	await dialog.getByRole('button', { name: 'Save types' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Watch is now taken. The phone pulls its history on its next sync.' })).toBeVisible();
	expect(devices.filterPuts[1].origins).toEqual([{ ...watch, mode: 'take' }]);

	const bandRow = page.getByRole('article', { name: 'Synthetic Band' });
	await bandRow.getByRole('button', { name: 'Choose types from Synthetic Band' }).click();
	dialog = page.getByRole('dialog', { name: 'Types from Synthetic Band' });
	await expect(dialog.getByLabel('Heart rate')).not.toBeChecked();
	await dialog.getByLabel('Sleep analysis').check();
	await dialog.getByRole('button', { name: 'Save types' }).click();
	await expect(bandRow).toContainText('Takes only Sleep analysis');
	expect(devices.filterPuts[2].origins).toEqual([{ ...watch, mode: 'take' }, { ...band, mode: 'per_type', types: ['HKCategoryTypeIdentifierSleepAnalysis'] }]);

	await bandRow.getByRole('button', { name: 'Choose types from Synthetic Band' }).click();
	dialog = page.getByRole('dialog', { name: 'Types from Synthetic Band' });
	await dialog.getByLabel('Sleep analysis').uncheck();
	await dialog.getByRole('button', { name: 'Save types' }).click();
	await expect(bandRow.getByRole('button', { name: 'Ignore' })).toHaveAttribute('aria-pressed', 'true');
	expect(devices.filterPuts[3].origins).toEqual([{ ...watch, mode: 'take' }, { ...band, mode: 'ignore' }]);

	// Cancel changes nothing.
	await bandRow.getByRole('button', { name: 'Choose types from Synthetic Band' }).click();
	await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByRole('dialog')).toHaveCount(0);
	expect(devices.filterPuts).toHaveLength(4);
});

test('a change made elsewhere: 409 reloads the filter and says so; the next choice goes through', async ({ page, devices }) => {
	await page.goto(sources);
	await expect(page.getByRole('article', { name: 'Synthetic Band' })).toBeVisible();
	devices.changeElsewhere();
	const bandRow = page.getByRole('article', { name: 'Synthetic Band' });
	await bandRow.getByRole('button', { name: 'Take' }).click();
	const alert = page.getByRole('alert');
	await expect(alert).toContainText('The source filter changed elsewhere, so it was reloaded.');
	await expect(alert).toContainText('reload it');
	expect(devices.filterPuts[0].version).toBe(3);
	// The control shows the server's state again, not the refused choice.
	await expect(bandRow.getByRole('button', { name: 'Ignore' })).toHaveAttribute('aria-pressed', 'true');

	await bandRow.getByRole('button', { name: 'Take' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Synthetic Band is now taken.' })).toBeVisible();
	expect(devices.filterPuts[1].version).toBe(4);
	await expect(page.getByRole('alert')).toHaveCount(0);
});

test('an app list not reported yet, and a revoked device, say so', async ({ page, devices }) => {
	devices.sourcesReportedAt = null;
	devices.apps = [];
	await page.goto(sources);
	await expect(page.getByText('The phone has not reported its apps yet.')).toBeVisible();
	await expect(page.getByText('No apps yet')).toBeVisible();

	await page.goto('/settings/devices/00000000-0000-4000-8000-0000000000d2/sources');
	await expect(page.getByRole('alert')).toContainText('no such active device');
});
