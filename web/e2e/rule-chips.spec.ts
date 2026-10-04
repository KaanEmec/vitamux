// The rule builder's one-click selectors (J15.6): origin, device type and relayed chips from the
// owner's data, saved as ordinary selectors of the rule.
import { mergeTests } from '@playwright/test';
import { test as devicesTest, expect } from './devices-fake';
import { test as rulesTest } from './rules-fake';

const test = mergeTests(rulesTest, devicesTest);

test('chips add watch-direct to a group and exclude relayed data', async ({ page, rules }) => {
	await page.goto('/rules/new?metric=heart_rate');
	await page.getByRole('button', { name: /Sources/ }).click();

	const first = page.getByRole('group', { name: 'Add a match from your sources' }).first();
	await expect(first.getByRole('button', { name: '+ provider apple_health, relayed' })).toBeVisible();
	await expect(first.getByRole('button', { name: '+ provider apple_health, origin app id com.example.synthetic.garmin' })).toBeVisible();
	await first.getByRole('button', { name: '+ provider apple_health, device type watch, not relayed' }).click();
	await page.getByRole('group', { name: 'Add a exclusion from your sources' }).getByRole('button', { name: '+ provider apple_health, relayed', exact: true }).click();

	await page.getByRole('button', { name: /Review/ }).click();
	await page.getByRole('button', { name: 'Save version' }).click();
	await expect(page).toHaveURL('/rules/heart_rate?saved=2');
	const spec = rules.posted[0] as { groups: { id: string; match: unknown[] }[]; exclude: unknown[] };
	expect(spec.groups[0].match).toContainEqual({ provider: 'apple_health', device_type: 'watch', relayed: false });
	expect(spec.exclude).toEqual([{ provider: 'apple_health', relayed: true }]);
});

test('the picker offers named sources and devices, and choosing one fills the selector', async ({ page, rules }) => {
	await page.goto('/rules/new?metric=heart_rate');
	await page.getByRole('button', { name: /Sources/ }).click();

	const match = page.getByRole('group', { name: 'Group 1', exact: true });
	const picker = match.getByLabel('Choose a source or device');
	// Apple's own devices keep their model names; a brand reaches direct and relayed devices alike.
	await expect(picker.getByRole('option')).toHaveText([
		'Choose a source or device…',
		'Apple Health (all data)',
		'Garmin Connect (all data)',
		'Apple Watch',
		'iPhone',
		'Garmin (any device)',
		'Garmin Forerunner 965',
		'My Forerunner'
	]);
	await expect(picker.getByRole('option', { name: /stand-in/ })).toHaveCount(0); // merged into My Forerunner

	await picker.selectOption({ label: 'Apple Watch' });
	await expect(match.getByLabel('Condition 1 value')).toHaveValue('apple_health');
	await expect(match.getByLabel('Condition 2 value')).toHaveValue('Apple Inc.');
	await expect(match.getByLabel('Condition 3 value')).toHaveValue('Watch');
	// An unchanged choice reads as its label in the review step.
	const review = page.getByRole('listitem').filter({ hasText: 'chest_strap' });
	await page.getByRole('button', { name: /Review/ }).click();
	await expect(review).toContainText('Apple Watch');

	// The raw fields stay editable: narrowing the choice makes it a plain selector again.
	await page.getByRole('button', { name: /Sources/ }).click();
	await match.getByRole('button', { name: 'And…' }).click();
	await match.getByLabel('Condition 4 field').selectOption('relayed');
	await page.getByRole('button', { name: /Review/ }).click();
	await expect(review).toContainText('provider apple_health, brand Apple, device model Watch, not relayed');
	await page.getByRole('button', { name: 'Save version' }).click();
	await expect(page).toHaveURL('/rules/heart_rate?saved=2');
	const spec = rules.posted[0] as { groups: { match: unknown[] }[] };
	expect(spec.groups[0].match).toEqual([{ provider: 'apple_health', device_manufacturer: 'Apple Inc.', device_model: 'Watch', relayed: false }]);
});

test('an owner-named device is chosen by id', async ({ page }) => {
	await page.goto('/rules/new?metric=heart_rate');
	await page.getByRole('button', { name: /Sources/ }).click();
	const match = page.getByRole('group', { name: 'Group 1', exact: true });
	await match.getByLabel('Choose a source or device').selectOption({ label: 'My Forerunner' });
	await expect(match.getByLabel('Condition 1 field')).toHaveValue('device_id');
	await expect(match.getByLabel('Condition 1 value')).toHaveValue('dev_00000000000000000000000000000004');
});
