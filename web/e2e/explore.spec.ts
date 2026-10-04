import { expect, fallbackDay, test } from './explore-fake';

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

test('metric detail: range, rollups, sources overlay, coverage and the rule lens', async ({ page }) => {
	await page.goto(detail);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Resting heart rate');
	await expect(page.getByText('Built-in rule')).toBeVisible();
	const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
	await expect(chart).toBeVisible();
	const values = page.getByRole('region', { name: 'Values' });
	await expect(values.getByRole('row')).toHaveCount(31); // header + first 30 days
	await expect(page.getByRole('list', { name: 'Statistics' }).getByText('30-day mean')).toBeVisible();
	await expect(page.getByRole('img', { name: /^garmin: data on/ })).toBeVisible(); // coverage strip

	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '1W' }).click();
	await expect(page).toHaveURL(/range=1W/);
	await expect(values.getByRole('row')).toHaveCount(8);

	// Each source's own values join the chart (legend and table fallback).
	await page.getByLabel('Show sources').check();
	await expect(page.getByText('Garmin · watch')).toBeVisible();
	await page.getByRole('region', { name: 'Resting heart rate chart' }).getByText('Show as a table').click();
	await expect(page.getByRole('table', { name: /Resting heart rate/ }).getByRole('columnheader')).toHaveCount(4); // date + resolved + 2 sources

	await page.getByRole('button', { name: 'How it’s calculated' }).click();
	await expect(page.getByRole('complementary', { name: 'How this is calculated' })).toBeVisible();

	// All: weekly rollups; a week opens its days.
	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: 'All' }).click();
	await expect(page.getByRole('group', { name: /weekly mean/ })).toBeVisible();
	await expect(values.getByRole('columnheader', { name: 'Week of' })).toBeVisible();
	await page.getByRole('group', { name: /weekly mean/ }).focus();
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL(/range=1W&end=2026-09-16/);
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
