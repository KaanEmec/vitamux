// Connections › <connection> › Devices (J20.7): type, name and merge of the devices a connection's
// records were measured on. The API is the stateful fake in connections-fake.ts; values are synthetic.
import { expect, ids, test } from './connections-fake';

test('devices tab: set a type and a name, then merge two devices of one ring', async ({ page, conns }) => {
	await page.goto(`/connections/${ids.ultrahuman}`);
	await page.getByRole('navigation', { name: 'Connection sections' }).getByRole('link', { name: 'Devices' }).click();
	await expect(page).toHaveURL(`/connections/${ids.ultrahuman}?tab=devices`);

	const table = page.getByRole('region', { name: 'Devices' });
	await expect(table.getByRole('row')).toHaveCount(3); // header + this connection's two devices, not the Withings scale
	const wearable = table.getByRole('row', { name: /ultrahuman:wearable/ });
	await expect(wearable.getByText('1,440 measurements · 7 sleep sessions')).toBeVisible();
	await expect(wearable.getByLabel('Type of ultrahuman:wearable')).toHaveValue('ring');

	await table.getByLabel('Type of 1000000001').selectOption('ring');
	await expect(page.getByRole('status').filter({ hasText: '1000000001 is a ring. Resolved values are being recomputed.' })).toBeVisible();
	await table.getByLabel('Name of 1000000001').fill('Synthetic ring');
	await table.getByLabel('Name of 1000000001').press('Enter');
	await expect(page.getByRole('status').filter({ hasText: 'Named Synthetic ring.' })).toBeVisible();
	expect(conns.devicePatches).toEqual([
		{ id: 'dev_' + '2'.repeat(32), body: { device_type: 'ring' } },
		{ id: 'dev_' + '2'.repeat(32), body: { name: 'Synthetic ring' } }
	]);

	await wearable.getByRole('button', { name: 'Merge into… (ultrahuman:wearable)' }).click();
	const dialog = page.getByRole('dialog', { name: 'Merge ultrahuman:wearable into another device' });
	await expect(dialog.getByLabel('Merge into')).toHaveValue('dev_' + '2'.repeat(32));
	await expect(dialog.getByText(/1,440 measurements · 7 sleep sessions, older versions included\) move to Synthetic ring/)).toBeVisible();
	const submit = dialog.getByRole('button', { name: 'Merge devices' });
	await expect(submit).toBeDisabled();
	await dialog.getByLabel('I understand that a merge cannot be undone here.').check();
	await submit.click();
	await expect(dialog).toBeHidden();
	expect(conns.merges).toEqual([{ id: 'dev_' + '1'.repeat(32), body: { into: 'dev_' + '2'.repeat(32) } }]);
	await expect(page.getByRole('status').filter({ hasText: 'ultrahuman:wearable merged into Synthetic ring: 1,440 measurements · 7 sleep sessions moved.' })).toBeVisible();

	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(table.getByRole('row', { name: /Synthetic ring/ }).getByText('1,452 measurements · 7 sleep sessions · 3 workouts')).toBeVisible();
	await expect(table.getByRole('button', { name: /Merge into/ })).toHaveCount(0);
	await expect(page.getByRole('list', { name: 'Merged devices' })).toContainText('ultrahuman:wearable was merged into Synthetic ring');
});

test('devices tab: a refused change shows its problem; a connection without devices says so', async ({ page, conns }) => {
	await page.goto(`/connections/${ids.ultrahuman}?tab=devices`);
	const name = page.getByRole('region', { name: 'Devices' }).getByLabel('Name of 1000000001');
	await name.fill('Synthetic ring');
	conns.sourceDevices[1].merged_into = 'dev_' + '1'.repeat(32); // merged meanwhile, e.g. in another tab
	await name.press('Enter');
	await expect(page.getByRole('alert')).toContainText('is merged into dev_' + '1'.repeat(32));
	expect(conns.devicePatches).toEqual([]);
	await expect(page.getByRole('list', { name: 'Merged devices' })).toContainText('1000000001 was merged into ultrahuman:wearable');

	await page.goto(`/connections/${ids.apple}?tab=devices`);
	await expect(page.getByText('No devices yet. They appear once this connection has stored records.')).toBeVisible();
});
