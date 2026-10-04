// The daily values and the all-sources day view, now under Explore (J21.8); /data redirects there.
import { expect, fallbackDay, test } from './explore-fake';

const range = '/explore/resting_heart_rate?range=1W&end=2026-09-16';
const drilldown = `/explore/resting_heart_rate/day/${fallbackDay}`;

test('a fallback day: see the reason, exclude an input, see the recalculation, revoke', async ({ page, data }) => {
	await page.goto(range);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Resting heart rate');

	const row = page.getByRole('row', { name: /Sep 14, 2026/ });
	await expect(row.getByText('Fallback')).toBeVisible();
	await expect(row.getByText('52 bpm', { exact: true })).toBeVisible();
	await expect(page.getByRole('row', { name: /Sep 13, 2026/ }).getByText('Direct')).toBeVisible();

	// The reason is in the explanation popover, opened and closed from the keyboard.
	await row.getByRole('button', { name: 'Explain' }).click();
	await expect(row.getByText('whoop had no value (stream degraded). Fell back to garmin: 52 bpm.')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(row.getByText('Fell back to garmin')).toBeHidden();

	await row.getByRole('link', { name: 'All sources' }).click();
	await expect(page).toHaveURL(drilldown);
	const result = page.getByRole('region', { name: 'Resolved value' });
	await expect(result.getByText('Fallback')).toBeVisible();
	await expect(result.getByText('52 bpm', { exact: true })).toBeVisible();
	await expect(result.getByText(/stream degraded/)).toBeVisible();

	// The rule inputs say why whoop was skipped; the sources table lists every source.
	const inputs = page.getByRole('region', { name: 'Rule inputs' });
	await expect(inputs.getByText(/schema_drift/)).toBeVisible();
	const sources = page.getByRole('region', { name: 'Sources' });
	await expect(sources.getByText('exclude: relayed=true')).toBeVisible();
	await expect(sources.getByRole('cell', { name: 'Excluded' })).toBeVisible();
	await expect(sources.getByRole('cell', { name: 'Used' })).toHaveCount(2);

	// Exclude garmin's record: the dialog is prefilled, the value recalculates.
	await inputs.getByRole('button', { name: 'Exclude record 9182736' }).click();
	const dialog = page.getByRole('dialog', { name: /Override resting_heart_rate/ });
	await expect(dialog.getByLabel('Record id')).toHaveValue('9182736');
	await dialog.getByRole('button', { name: 'Save override' }).click();
	await expect(dialog).toBeHidden();
	await expect(result.getByText('54 bpm', { exact: true })).toBeVisible();
	await expect(result.getByText(/Fell back to apple_watch/)).toBeVisible();
	expect(data.created).toEqual([
		{ metric: 'resting_heart_rate', window: { kind: 'local_day', key: fallbackDay, local_date: fallbackDay }, action: 'exclude_input', input_id: '9182736' }
	]);

	const overrides = page.getByRole('region', { name: 'Overrides for this day' });
	await expect(overrides.locator('strong', { hasText: 'Exclude input 9182736' })).toBeVisible();
	await overrides.getByRole('button', { name: /Revoke/ }).click();
	await expect(result.getByText('52 bpm', { exact: true })).toBeVisible();
	await expect(result.getByText(/Fell back to garmin/)).toBeVisible();
	await expect(overrides.getByRole('button', { name: /Revoke/ })).toBeHidden();
	await expect(overrides.getByText(/revoked/)).toBeVisible();
});

test('set a value with a note, then revoke it', async ({ page }) => {
	await page.goto(drilldown);
	const result = page.getByRole('region', { name: 'Resolved value' });
	await expect(result.getByText('52 bpm', { exact: true })).toBeVisible();

	await result.getByRole('button', { name: 'Set a value…' }).click();
	const dialog = page.getByRole('dialog', { name: /Override resting_heart_rate/ });
	await dialog.getByRole('spinbutton', { name: 'Value' }).fill('60');
	await dialog.getByLabel('Note').fill('manual cuff reading');
	await dialog.getByRole('button', { name: 'Save override' }).click();
	await expect(result.getByText('Overridden')).toBeVisible();
	await expect(result.getByText('60 bpm', { exact: true })).toBeVisible();

	await page.getByRole('button', { name: /Revoke/ }).click();
	await expect(result.getByText('Fallback')).toBeVisible();
	await expect(result.getByText('52 bpm', { exact: true })).toBeVisible();
});

test('a rejected override shows the field error and keeps the dialog open', async ({ page }) => {
	await page.goto(drilldown);
	await page.getByRole('button', { name: 'Exclude an input…' }).click();
	const dialog = page.getByRole('dialog', { name: /Override/ });
	await dialog.getByLabel('Record id').fill('');
	// The browser's own required check would stop an empty submit; relax it to reach the API.
	await dialog.getByLabel('Record id').evaluate((el: HTMLInputElement) => (el.required = false));
	await dialog.getByRole('button', { name: 'Save override' }).click();
	await expect(dialog.getByText('is required for exclude_input')).toBeVisible();
	await expect(dialog).toBeVisible();
});

test('the provenance chain opens from a source and from a record', async ({ page }) => {
	await page.goto(drilldown);
	await page.getByRole('region', { name: 'Rule inputs' }).getByRole('button', { name: 'Provenance of record 9182736' }).click();
	const dialog = page.getByRole('dialog', { name: /Provenance of measurement 9182736/ });
	await expect(dialog.getByText('Earlier version')).toBeVisible();
	await expect(dialog.getByText('This version')).toBeVisible();
	await expect(dialog.getByText('garmin.daily_summary@3')).toHaveCount(2);
	await page.keyboard.press('Escape');
	await expect(dialog).toBeHidden();

	await page.getByRole('region', { name: 'Sources' }).getByRole('button', { name: /Trace a record of garmin/ }).click();
	await expect(page.getByRole('dialog', { name: /Provenance of measurement/ })).toBeVisible();
});

test('the resolved endpoints being unavailable is shown, not fatal', async ({ page, data }) => {
	data.resolvedStatus = 503;
	await page.goto(range);
	await expect(page.getByRole('alert').first()).toContainText('resolved values are not ready yet');
	await expect(page.getByRole('link', { name: 'All sources' }).first()).toBeVisible();
});

test('a heart-rate day (the Day chart) renders in under 500 ms', async ({ page }) => {
	await page.goto('/explore/heart_rate/day/' + fallbackDay);
	await expect(page.getByRole('group', { name: /Heart rate on/ })).toBeVisible();
	const ms = await page.evaluate(async () => {
		// The chart marks its own data-join-to-paint time (ChartFrame.svelte).
		for (let i = 0; i < 50 && !performance.getEntriesByName('vx-chart-render').length; i++) {
			await new Promise((r) => setTimeout(r, 20));
		}
		return performance.getEntriesByName('vx-chart-render')[0].duration;
	});
	console.log(`Heart-rate day chart render: ${ms.toFixed(1)} ms`);
	expect(ms).toBeLessThan(500);
	// The resolved buckets and both sources are in the chart's table fallback (and its legend, each with its own dash and colour).
	await page.getByText('Show as a table').click();
	await expect(page.getByRole('table', { name: /Heart rate on/ }).getByRole('columnheader')).toHaveCount(4); // time + resolved + 2 sources
});

test('old /data links redirect to Explore', async ({ page }) => {
	for (const [from, to] of [
		['/data?metric=resting_heart_rate&start=2026-09-12&end=2026-09-16', '/explore/resting_heart_rate?end=2026-09-16'],
		[`/data/day/resting_heart_rate/${fallbackDay}`, drilldown],
		[`/data/sleep?date=${fallbackDay}`, `/explore/sleep?date=${fallbackDay}`],
		['/data/workouts?start=2026-09-12&end=2026-09-16', '/explore/workouts?start=2026-09-12&end=2026-09-16'],
		['/data', '/explore']
	]) {
		await page.goto(from);
		await expect(page).toHaveURL(to);
	}
});
