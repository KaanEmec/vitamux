import { mergeTests } from '@playwright/test';
import { expect, fallbackDay, test } from './explore-fake';
import { test as rulesTest } from './rules-fake';

const lensTest = mergeTests(test, rulesTest);

const detail = `/explore/resting_heart_rate?range=1M&end=2026-09-16`;

test('the inventory lists every kind by section, each opening its view', async ({ page }) => {
	await page.goto('/explore');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Explore');
	await expect(page.getByText('4 metrics · 1 event type · 1 lab analyte', { exact: false })).toBeVisible();

	const heart = page.getByRole('region', { name: 'Heart and circulation' });
	const row = heart.getByRole('row', { name: /Resting heart rate/ });
	await expect(row.getByRole('cell', { name: '50 bpm' })).toBeVisible();
	await expect(row.getByText('Whoop')).toBeVisible();
	await expect(row.getByRole('cell', { name: '90', exact: true })).toBeVisible();

	const links: [string, string][] = [
		['Resting heart rate', '/explore/resting_heart_rate'],
		['Blood pressure', '/explore/blood-pressure'],
		['Weight', '/explore/weight'],
		['Sleep', '/explore/sleep'],
		['Workouts', '/explore/workouts'],
		['Irregular rhythm', '/explore/events?code=irregular_rhythm'],
		['LDL cholesterol', '/lab/analytes/ldl_c']
	];
	for (const [name, href] of links) await expect(page.getByRole('link', { name, exact: true })).toHaveAttribute('href', href);
	await expect(page.getByRole('region', { name: 'Blood pressure' }).getByText('121/79')).toBeVisible();

	// Sections collapse.
	await heart.getByRole('button', { name: 'Heart and circulation' }).click();
	await expect(heart.getByRole('table')).toBeHidden();
});

test('filters: text, source, device, origin and catalogue items without data', async ({ page }) => {
	await page.goto('/explore');
	const names = page.locator('a.name');
	await expect(names).toHaveCount(9);

	await page.getByRole('searchbox', { name: 'Search' }).fill('heart');
	await expect(names).toHaveText(['Heart rate', 'Resting heart rate']);
	await page.getByRole('searchbox', { name: 'Search' }).fill('scale'); // a device model
	await expect(names).toHaveText(['Weight']);
	await page.getByRole('searchbox', { name: 'Search' }).fill('');

	await page.getByRole('group', { name: 'Source' }).getByRole('button', { name: 'Withings' }).click();
	await expect(names).toHaveText(['Weight', 'Blood pressure']);
	await page.getByRole('button', { name: 'All sources' }).click();

	await page.getByLabel('Device').selectOption({ label: 'Synthetic Watch' });
	await expect(names).toHaveCount(4);
	await page.getByLabel('Device').selectOption('');
	await page.getByLabel('Origin app').selectOption({ label: 'Example Health' });
	await expect(names).toHaveText(['Steps', 'Irregular rhythm']);
	await page.getByLabel('Origin app').selectOption('');

	await page.getByLabel('Show catalogue items with no data').check();
	await expect(page.getByRole('link', { name: 'Spo2' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Bp systolic' })).toBeVisible();
});

test('?origin= sets the origin filter; ignored sources are hidden until toggled and listed on their own', async ({ page, explore }) => {
	await page.goto('/explore?origin=com.example.health');
	const names = page.locator('a.name');
	await expect(names).toHaveText(['Steps', 'Irregular rhythm']);
	await expect(page.getByLabel('Origin app')).toHaveValue('com.example.health');
	const ignored = page.getByRole('region', { name: 'Ignored sources' });
	await expect(ignored).toHaveCount(0);
	expect(explore.inventoryQueries).toEqual(['']);

	await page.getByRole('switch', { name: 'Ignored sources' }).check();
	await expect(ignored.getByText('No ignored records match these filters.')).toBeVisible();
	expect(explore.inventoryQueries).toEqual(['', 'include_ignored=true']);
	await page.getByLabel('Origin app').selectOption('');
	const rows = ignored.getByRole('row');
	await expect(rows).toHaveCount(3);
	await expect(rows.nth(1)).toContainText('Heart rate');
	await expect(rows.nth(1)).toContainText('Synthetic Band');
	await expect(rows.nth(1).getByRole('cell', { name: '1,440' })).toBeVisible();
	await expect(rows.nth(2)).toContainText('Sleep');
	// Nothing held raw mixes into the stored items.
	await expect(names).toHaveCount(9);

	// An origin only the ignored records name (a link from a device's sources).
	await page.goto('/explore?origin=com.example.synthetic.band');
	await expect(page.getByText('Nothing matches these filters')).toBeVisible();
	await expect(page.getByLabel('Origin app')).toHaveValue('com.example.synthetic.band');
	await page.getByRole('switch', { name: 'Ignored sources' }).check();
	await expect(page.getByRole('region', { name: 'Ignored sources' }).getByRole('row')).toHaveCount(3);
	await expect(names).toHaveCount(0);
});

test('metric detail: the ignored sources toggle lists origins held raw under the chart', async ({ page, explore }) => {
	await page.goto(detail);
	const toggle = page.getByRole('group', { name: 'Series' }).getByRole('button', { name: 'Ignored sources' });
	await expect(toggle).toHaveAttribute('aria-pressed', 'false');
	await expect(page.getByRole('region', { name: 'Ignored sources' })).toHaveCount(0);
	expect(explore.seriesQueries.some((q) => q.includes('include_ignored'))).toBe(false);
	await toggle.click();
	await expect(toggle).toHaveAttribute('aria-pressed', 'true');
	await expect(page.getByRole('region', { name: 'Ignored sources' })).toContainText('Synthetic Band: 96 records held raw, ignored by the device’s source filter');
	expect(explore.seriesQueries.at(-1)).toContain('include_ignored=true');
	// The chart's source toggles are unchanged: no ignored origin is plotted.
	await expect(page.getByRole('group', { name: 'Series' }).getByRole('button', { name: /Synthetic Band/ })).toHaveCount(0);
	await toggle.click();
	await expect(page.getByRole('region', { name: 'Ignored sources' })).toHaveCount(0);
});

test('pin a metric to the dashboard', async ({ page, explore }) => {
	await page.goto('/explore');
	const pin = page.getByRole('button', { name: 'Pin Resting heart rate to the dashboard' });
	await expect(pin).toHaveAttribute('aria-pressed', 'false');
	await expect(page.getByRole('button', { name: 'Pin Steps to the dashboard' })).toHaveAttribute('aria-pressed', 'true');
	await pin.click();
	await expect(pin).toHaveAttribute('aria-pressed', 'true');
	expect(explore.saved.at(-1)).toEqual({
		version: 1,
		cards: [
			{ metric: 'steps', size: 'M', hidden: false },
			{ metric: 'resting_heart_rate', size: 'M', hidden: false }
		]
	});
});

test('metric detail: stats header, range, overlays, compare, source strip and the lens', async ({ page }) => {
	await page.goto(detail);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Resting heart rate');
	await expect(page.getByRole('link', { name: /^Built-in rule/ })).toHaveAttribute('href', '/rules/resting_heart_rate');

	// Latest with its delta to the 30-day mean, the period mean against the period before, range and coverage.
	const stats = page.getByRole('list', { name: 'Statistics' }).getByRole('listitem');
	await expect(stats).toHaveCount(4);
	await expect(stats.nth(0)).toContainText(/Latest\s*50\s*bpm.*Sep 16, 2026 · −1 vs 30-day mean/);
	await expect(stats.nth(1)).toContainText(/30-day mean\s*51\s*bpm\s*\+2 vs previous 30 days/);
	await expect(stats.nth(2)).toContainText('50–52');
	await expect(stats.nth(3)).toContainText(/30\s*\/ 30/);

	const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
	await expect(chart).toBeVisible();
	const region = page.getByRole('region', { name: 'Resting heart rate chart' });
	await expect(region.getByText('7-day range')).toBeVisible();
	// The source behind each day is a toggle, off until chosen and kept for the next visit.
	const strip = page.getByRole('img', { name: /^Source per day/ });
	const stripToggle = page.getByRole('group', { name: 'Series' }).getByRole('button', { name: 'Source per day' });
	await expect(stripToggle).toHaveAttribute('aria-pressed', 'false');
	await expect(strip).toHaveCount(0);
	await stripToggle.click();
	await expect(strip).toHaveAccessibleName('Source per day: Whoop 29, Garmin 1, none 0 of 30 days');
	await page.reload();
	await expect(stripToggle).toHaveAttribute('aria-pressed', 'true');
	await expect(strip).toBeVisible();
	const values = page.getByRole('region', { name: 'Values' });
	await expect(values.getByRole('row')).toHaveCount(31); // header + first 30 days
	await expect(values.getByRole('row', { name: /Sep 14, 2026/ })).toContainText(/52 bpm\s*Fallback\s*Garmin\s*Built-in/);

	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '1W' }).click();
	await expect(page).toHaveURL(/range=1W/);
	await expect(values.getByRole('row')).toHaveCount(8);
	await expect(stats.nth(1)).toContainText('7-day mean');

	// Source overlays are toggles, one per provider; the previous period is a dashed overlay.
	const toggles = page.getByRole('group', { name: 'Series' });
	const garmin = toggles.getByRole('button', { name: 'Garmin' });
	await expect(garmin).toHaveAttribute('aria-pressed', 'false');
	await garmin.click();
	await expect(garmin).toHaveAttribute('aria-pressed', 'true');
	await expect(region.getByText('Garmin · watch')).toBeVisible();
	await page.getByRole('button', { name: 'Compare previous' }).click();
	await expect(region.getByText('Previous 7 days', { exact: true })).toBeVisible(); // not the stat's "vs previous 7 days"
	await region.getByText('Show as a table').click();
	const table = page.getByRole('table', { name: /Resting heart rate/ });
	await expect(table.getByRole('columnheader')).toHaveCount(4); // date + resolved + previous + garmin
	await toggles.getByRole('button', { name: 'Apple Health' }).click();
	await expect(table.getByRole('columnheader')).toHaveCount(5);
	await garmin.click();
	await expect(region.getByText('Garmin · watch')).toBeHidden();

	await page.getByRole('button', { name: 'How it’s calculated' }).click();
	await expect(page.getByRole('complementary', { name: 'How this is calculated' })).toBeVisible();

	// All: weekly rollups; a week opens its days. Compare previous needs a fixed range.
	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: 'All' }).click();
	await expect(page.getByRole('group', { name: /weekly mean/ })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Compare previous' })).toBeDisabled();
	await expect(values.getByRole('columnheader', { name: 'Week of' })).toBeVisible();
	await page.getByRole('group', { name: /weekly mean/ }).focus();
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/range=1W&end=2026-09-16/);
});

lensTest('the rule lens beside the chart: window counts, draft preview, save and revert', async ({ page, rules }) => {
	rules.preview = 'ok';
	await page.goto('/explore/resting_heart_rate?range=1W&end=2026-09-16');
	await page.getByRole('button', { name: 'How it’s calculated' }).click();
	const lens = page.getByRole('complementary', { name: 'How this is calculated' });
	await expect(lens.getByText('Built-in · active')).toBeVisible();
	await expect(lens.getByText(/^For each day, use the first source in order with data/)).toBeVisible();
	const items = lens.getByRole('list', { name: 'Source priority' }).getByRole('listitem');
	await expect(items.filter({ hasText: 'garmin' })).toContainText('1 day');
	await expect(items.filter({ hasText: 'oura' })).toContainText('0 days');

	// A draft previews in the lens (mini chart and summary) and on the chart as a ghost.
	await items.filter({ hasText: 'garmin' }).dragTo(items.filter({ hasText: 'oura' }));
	await expect(items).toContainText(['garmin', 'oura']);
	await expect(lens.getByText('3 of 14 days change')).toBeVisible();
	await expect(lens.getByRole('img', { name: /Active rule \(solid\) and draft \(dashed\)/ })).toBeVisible();
	await lens.locator('summary', { hasText: 'Days that change' }).click();
	await expect(lens.getByRole('table', { name: 'Days that change' }).getByRole('row')).toHaveCount(4);
	await expect(page.getByRole('region', { name: 'Resting heart rate chart' }).getByText('Draft rule')).toBeVisible();

	await lens.getByRole('button', { name: 'Discard' }).click();
	await expect(items).toContainText(['oura', 'garmin']);
	await expect(page.getByRole('region', { name: 'Resting heart rate chart' }).getByText('Draft rule')).toBeHidden();

	// Only what the metric allows is offered.
	await expect(lens.getByLabel('Strategy').locator('option')).toHaveText(['First available']);
	await lens.getByRole('button', { name: 'Move garmin up' }).click();
	await lens.getByRole('button', { name: 'Save and activate' }).click();
	await expect(lens.getByRole('status')).toContainText('Version 2 is now active.');
	expect((rules.posted.at(-1)?.groups as { id: string }[]).map((g) => g.id)).toEqual(['garmin', 'oura']);
	await expect(lens.getByText('Rule v2 · active')).toBeVisible();

	// The history reverts to the earlier version.
	const history = lens.getByRole('region', { name: 'History' });
	await history.getByRole('button', { name: 'Revert to version 1' }).click();
	await expect(lens.getByRole('status')).toContainText('Reverted: version 1 is active again.');
	await expect(lens.getByText('Rule v1 · active')).toBeVisible();
	await expect(history.getByRole('button', { name: 'Activate version 2' })).toBeVisible();
});

test('nothing resolved: the sources own series are drawn, with the reason', async ({ page }) => {
	await page.goto('/explore/spo2?range=1M&end=2026-09-16'); // no resolved value; the sources have points
	const region = page.getByRole('region', { name: 'Spo2 chart' });
	await expect(page.getByRole('group', { name: /each source per day/ })).toBeVisible();
	await expect(page.getByText('No values in this range')).toHaveCount(0);
	await expect(page.getByText(/Nothing resolved in this range, so each source’s own values are shown/)).toBeVisible();
	const toggles = page.getByRole('group', { name: 'Series' });
	for (const name of ['Garmin', 'Apple Health']) await expect(toggles.getByRole('button', { name })).toHaveAttribute('aria-pressed', 'true');
	await expect(region).toBeVisible();
});

test('a metric without resolved or source values still says so', async ({ page, explore }) => {
	explore.noSources = true;
	await page.goto('/explore/spo2?range=1M&end=2026-09-16');
	await expect(page.getByText('No values in this range')).toBeVisible();
});

test('metric detail at 390 px: no page scroll, the lens stacks under the chart', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto(detail);
	await expect(page.getByRole('group', { name: /resolved per day/ })).toBeVisible();
	await page.getByRole('button', { name: 'How it’s calculated' }).click();
	const lens = page.getByRole('complementary', { name: 'How this is calculated' });
	await expect(lens).toBeVisible(); // inline, not a sheet
	const chart = await page.getByRole('region', { name: 'Resting heart rate chart' }).boundingBox();
	const panel = await lens.boundingBox();
	expect(panel!.y).toBeGreaterThan(chart!.y + chart!.height);
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
	await page.goto('/explore');
	await expect(page.getByRole('link', { name: 'Resting heart rate', exact: true })).toBeVisible();
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
});

test('a point opens its explanation, provenance and override', async ({ page, data }) => {
	await page.goto(detail);
	const values = page.getByRole('region', { name: 'Values' });
	const row = values.getByRole('row', { name: /Sep 14, 2026/ });
	await expect(row.getByText('Fallback')).toBeVisible();
	await row.getByRole('button', { name: 'Explain' }).click();
	await expect(row.getByText(/Fell back to garmin: 52 bpm/)).toBeVisible();
	await page.keyboard.press('Escape');

	await row.getByRole('button', { name: `Details of ${fallbackDay}` }).click();
	const panel = page.getByRole('region', { name: /Sep 14, 2026/ });
	await expect(panel.getByText('whoop had no value (stream degraded). Fell back to garmin: 52 bpm.')).toBeVisible();
	await panel.getByRole('button', { name: 'Provenance of record 9182736' }).click();
	await expect(page.getByRole('dialog', { name: /Provenance of measurement 9182736/ })).toBeVisible();
	await page.keyboard.press('Escape');

	await panel.getByRole('button', { name: 'Set a value…' }).click();
	const dialog = page.getByRole('dialog', { name: /Override resting_heart_rate/ });
	await dialog.getByRole('spinbutton', { name: 'Value' }).fill('60');
	await dialog.getByLabel('Note').fill('manual cuff reading');
	await dialog.getByRole('button', { name: 'Save override' }).click();
	await expect(dialog).toBeHidden();
	await expect(panel.getByText('Overridden')).toBeVisible();
	await expect(row.getByRole('cell', { name: '60 bpm' })).toBeVisible();
	expect(data.created.at(-1)).toMatchObject({ metric: 'resting_heart_rate', window: { kind: 'local_day', key: fallbackDay, local_date: fallbackDay }, action: 'set_value', value: 60 });

	// The chart opens a point from the keyboard too: the focused point is the last day.
	await page.getByRole('group', { name: /resolved per day/ }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('region', { name: /Sep 16, 2026/ })).toBeVisible();
	await page.getByRole('region', { name: /Sep 16, 2026/ }).getByRole('link', { name: 'All sources and overrides' }).click();
	await expect(page).toHaveURL('/explore/resting_heart_rate/day/2026-09-16');
});

test('additive metrics are bars; unknown codes say so', async ({ page }) => {
	await page.goto('/explore/steps?range=1W&end=2026-09-16');
	await expect(page.getByRole('group', { name: 'Steps, resolved per day' })).toBeVisible();
	await expect(page.getByRole('group', { name: 'Steps, resolved per day' }).locator('rect.bar')).toHaveCount(7);
	await page.goto('/explore/not_a_metric');
	await expect(page.getByText('No metric with the code not_a_metric')).toBeVisible();
});
