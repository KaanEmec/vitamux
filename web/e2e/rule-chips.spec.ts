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
