// The rule lens (J21.10) on the Rules metric page: reorder, preview, save and activate, revert;
// the sum acknowledgement; server field errors on their control; the plain sentence per strategy.
import { expect, test } from './rule-lens-fake';

type Json = Record<string, unknown>;

const lensOf = (page: import('@playwright/test').Page) => page.getByRole('complementary', { name: 'How this is calculated' });

test('reorder, preview, save and activate, then revert', async ({ page, rules }) => {
	rules.preview = 'ok';
	await page.goto('/rules/heart_rate');
	const lens = lensOf(page);
	await expect(lens.getByText('For each 5-minute bucket, use the first source in order with data; if none has, the 5-minute bucket has no value.')).toBeVisible();
	await expect(lens.getByText('Change the rule to preview it')).toBeVisible();
	expect(rules.previews).toHaveLength(0);

	// Drag garmin to the top, then move Apple Watch up with its button.
	const items = lens.getByRole('list', { name: 'Source priority' }).getByRole('listitem');
	await items.filter({ hasText: 'garmin' }).dragTo(items.filter({ hasText: 'chest_strap' }));
	await expect(items).toContainText(['garmin', 'chest_strap', 'Apple Watch']);
	await lens.getByRole('button', { name: 'Move Apple Watch up' }).click();
	await expect(items).toContainText(['garmin', 'Apple Watch', 'chest_strap']);
	await expect(lens.getByRole('button', { name: 'Move Apple Watch up' })).toBeFocused();
	await expect(lens.getByText('Draft · not saved')).toBeVisible();

	// The preview covers the page's 30 days and summarises the changed days.
	await expect(lens.getByText('3 of 14 days change')).toBeVisible();
	await expect(lens.getByText(/mean \+0\.3 bpm · no new gaps/)).toBeVisible();
	const changes = lens.getByRole('table', { name: 'Days that change' });
	await expect(changes.getByRole('row')).toHaveCount(4);
	await expect(changes.getByRole('row').nth(1)).toContainText('61.5 bpm · garmin');
	const body = rules.previews.at(-1) as { spec: Json; start_date: string; end_date: string };
	expect((body.spec.groups as { id: string }[]).map((g) => g.id)).toEqual(['garmin', 'apple_watch', 'chest_strap']);
	expect((Date.parse(body.end_date) - Date.parse(body.start_date)) / 86_400_000).toBe(29);

	await lens.getByLabel('Note (optional)').fill('garmin first');
	await lens.getByRole('button', { name: 'Save and activate' }).click();
	await expect(lens.getByRole('status')).toContainText('Version 2 is now active.');
	expect((rules.posted[0].groups as { id: string }[]).map((g) => g.id)).toEqual(['garmin', 'apple_watch', 'chest_strap']);
	await expect(page.getByRole('heading', { name: 'In effect: version 2' })).toBeVisible();
	await expect(lens.getByText('Draft · not saved')).toBeHidden();

	await lens.getByRole('button', { name: 'Revert to version 1' }).click();
	await expect(lens.getByRole('status')).toContainText('Reverted: version 1 is active again.');
	await expect(page.getByRole('heading', { name: 'In effect: version 1' })).toBeVisible();
	await expect(items).toContainText(['chest_strap', 'Apple Watch', 'garmin']);

	// Both changes are in the version history.
	const history = page.getByRole('region', { name: 'Version history' });
	await expect(history.getByRole('listitem').filter({ hasText: 'rule:heart_rate:2' })).toContainText('garmin first');
	await expect(history.getByRole('listitem').filter({ hasText: 'rule:heart_rate:1' })).toContainText('Active');
});

test('the lens offers only what the metric allows', async ({ page }) => {
	await page.goto('/rules/heart_rate');
	const lens = lensOf(page);
	await expect(lens.getByLabel('Window')).toHaveText(/Fixed buckets.*Local hour.*Local day/s);
	await expect(lens.getByLabel('Window').locator('option')).toHaveCount(3);
	await expect(lens.getByRole('radio', { name: 'Sum' })).toHaveCount(0);
	await expect(lens.getByText("Sum isn't offered")).toBeVisible();
});

test('an unacknowledged sum cannot be saved', async ({ page, rules }) => {
	await page.goto('/rules/steps');
	const lens = lensOf(page);
	await lens.getByRole('radio', { name: 'Sum' }).check();
	await expect(lens.getByText('Adding sources can count the same activity twice.')).toBeVisible();
	await lens.getByRole('button', { name: 'Save and activate' }).click();

	const ack = lens.getByLabel('I understand the duplicate risk');
	await expect(lens.getByText('Confirm that you understand the duplicate risk before saving a sum.')).toBeVisible();
	await expect(ack).toBeFocused();
	await expect(ack).toHaveAttribute('aria-invalid', 'true');
	expect(rules.posted).toHaveLength(0);

	await ack.check();
	await lens.getByRole('button', { name: 'Save and activate' }).click();
	await expect(lens.getByRole('status')).toContainText('Version 2 is now active.');
	expect(rules.posted[0].strategy).toEqual({ op: 'sum_across_sources' });
	expect(rules.posted[0].acknowledged_warnings).toEqual(['cross_source_sum_duplicate_risk']);
});

test('a server field error points back to its control', async ({ page, rules }) => {
	await page.goto('/rules/heart_rate');
	const lens = lensOf(page);
	const coverage = lens.getByLabel(/Minimum coverage/);
	await coverage.fill('95');
	await expect(lens.getByText('Minimum coverage · 95%')).toBeVisible();
	await lens.getByRole('button', { name: 'Save as version 2' }).click();

	await expect(coverage).toHaveAttribute('aria-invalid', 'true');
	await expect(coverage).toBeFocused();
	await expect(coverage).toHaveAccessibleDescription('must be at most 0.9');
	await expect(lens.getByRole('alert')).toContainText('invalid rule');
	expect((rules.posted[0].quality as Json).min_coverage).toBe(0.95);
});

test('every strategy reads as a plain sentence', async ({ page }) => {
	await page.goto('/rules/steps');
	const lens = lensOf(page);
	const sentences: [string, string][] = [
		['One source', 'For each day, use only watch; without it, the day has no value.'],
		['Mean', 'For each day, average the sources.'],
		['Lowest', 'For each day, take the lowest of the sources.'],
		['Highest', 'For each day, take the highest of the sources.'],
		['Sum', 'For each day, add the sources together; the same activity can be counted twice.'],
		['Latest', 'For each day, use the newest value of the sources; ties go by order.'],
		['Earliest', 'For each day, use the oldest value of the sources; ties go by order.'],
		['First available', 'For each day, use the first source in order with data; if none has, the day has no value.']
	];
	for (const [pill, sentence] of sentences) {
		await lens.getByRole('radio', { name: pill }).check();
		await expect(lens.getByText(sentence)).toBeVisible();
	}
	await expect(lens.getByText('Each hour is picked first, then the hours are added up.')).toBeVisible();
	await lens.getByLabel(/Minimum coverage/).fill('60');
	await expect(lens.getByText('For each day, use the first source in order with at least 60% coverage; if none has, the day has no value.')).toBeVisible();
	await lens.getByLabel('Add an exclusion').selectOption('provider apple_health, relayed');
	await expect(lens.getByText('1 excluded source is never used.')).toBeVisible();
	await lens.getByRole('button', { name: 'Remove exclusion provider apple_health, relayed' }).click();
	await expect(lens.getByText('1 excluded source is never used.')).toBeHidden();
});

test('a bottom sheet on a phone', async ({ page }) => {
	await page.setViewportSize({ width: 375, height: 812 });
	await page.goto('/rules/heart_rate');
	await page.getByRole('button', { name: 'How this is calculated' }).click();
	const sheet = page.getByRole('dialog', { name: 'How this is calculated' });
	await expect(sheet.getByText(/^For each 5-minute bucket/)).toBeVisible();
	await sheet.getByRole('button', { name: 'Move garmin up' }).click();
	await sheet.getByRole('button', { name: 'Close' }).click();
	await expect(page.getByRole('button', { name: /How this is calculated.*Draft/ })).toBeVisible();
});

test('named sources can be excluded and read by their names', async ({ page }) => {
	await page.goto('/rules/heart_rate');
	const lens = lensOf(page);
	await expect(lens.getByRole('listitem').filter({ hasText: 'Apple Watch' })).toBeVisible();
	await lens.getByLabel('Add an exclusion').selectOption('Garmin (any device)');
	await expect(lens.getByRole('button', { name: 'Remove exclusion Garmin (any device)' })).toBeVisible();
	await expect(lens.getByLabel('Add an exclusion').getByRole('option', { name: 'Garmin (any device)' })).toHaveCount(0);
});
