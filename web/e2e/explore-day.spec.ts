// The Day view (J26.3): the 1D range on metrics with `intraday`, zooming through the ladder,
// overlays, the bucket panel, the table fallback and the drill-ins.
import type { Locator, Page } from '@playwright/test';
import { expect, test } from './explore-fake';

const day = '/explore/heart_rate?range=1D&end=2026-09-14';

/** Drag across the plot from `a` to `b` (fractions of its width). */
async function drag(page: Page, chart: Locator, a: number, b: number) {
	const box = (await chart.boundingBox())!;
	const left = box.x + 48; // the frame's left margin
	const w = box.width - 48 - 12;
	await page.mouse.move(left + w * a, box.y + box.height / 2);
	await page.mouse.down();
	await page.mouse.move(left + w * b, box.y + box.height / 2, { steps: 4 });
	await page.mouse.up();
}

test('heart rate zooms from 1-minute buckets through 30 s to raw readings, with overlays and the bucket panel', async ({ page, explore }) => {
	await page.goto(day);
	await expect(page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '1D' })).toHaveAttribute('aria-pressed', 'true');
	const chart = page.getByRole('group', { name: /^Heart rate on .*Sep 14, 2026, 1-minute buckets/ });
	await expect(chart).toBeVisible();
	const region = page.getByRole('region', { name: 'Heart rate chart' });
	await expect(region.getByText('Min–max per bucket')).toBeVisible();
	const overlays = page.getByRole('list', { name: 'Overlays' });
	await expect(overlays.getByRole('listitem')).toHaveText([/^Night /, /^Running /]);
	await expect(chart.locator('rect.night')).toHaveCount(1);
	await expect(chart.locator('rect.workout')).toHaveCount(1);

	// Source series are toggles; Garmin (every 2 min) is never drawn finer than 5-minute buckets.
	await page.getByRole('group', { name: 'Series' }).getByRole('button', { name: 'Garmin' }).click();
	await expect(region.getByText('Garmin · Synthetic Watch, 5-minute buckets')).toBeVisible();

	// The source strip, when toggled.
	await page.getByRole('group', { name: 'Series' }).getByRole('button', { name: 'Source per bucket' }).click();
	await expect(page.getByRole('img', { name: /^Source per bucket: Whoop 1440, none 0 of 1440 buckets/ })).toBeVisible();

	// 30 minutes of the day: 30-second buckets.
	await drag(page, chart, 0.5, 0.5 + 1 / 48);
	const fine = page.getByRole('group', { name: /^Heart rate on .*Sep 14, 2026, 30-second buckets/ });
	await expect(fine).toBeVisible();
	// Half of that: raw readings, with the resolved line at the finest bucket.
	await drag(page, fine, 0.2, 0.6);
	const raw = page.getByRole('group', { name: /^Heart rate on .*Sep 14, 2026, raw readings/ });
	await expect(raw).toBeVisible();
	await expect(region.getByText('Resolved, 30-second buckets')).toBeVisible();
	expect(explore.intraday).toEqual(expect.arrayContaining(['sources 1m', 'sources 5m', 'resolved 1m', 'sources 30s', 'resolved 30s', 'sources raw']));

	// A bucket shows its value, span and source; at raw zoom the readings in it with their device and origin.
	await raw.focus(); // already focused by the drag: End picks the last bucket
	await page.keyboard.press('End');
	await page.keyboard.press('Enter');
	const panel = page.getByRole('region', { name: /^Bucket / });
	await expect(panel.getByText('30-second bucket')).toBeVisible();
	await expect(panel.getByText(/bpm/).first()).toBeVisible();
	await expect(panel.getByText(/^From Whoop/)).toBeVisible();
	const readings = panel.getByRole('table', { name: /^Readings from/ });
	await expect(readings.getByRole('row').nth(1)).toContainText('Whoop · Synthetic Band');
	await expect(panel.getByRole('link', { name: /Explanation and all sources/ })).toHaveAttribute('href', '/explore/heart_rate/day/2026-09-14');

	// The table fallback, then back to the whole day.
	await region.getByText('Show as a table').first().click();
	await expect(region.getByRole('table', { name: /raw readings/ })).toBeVisible();
	await region.getByRole('button', { name: 'Reset zoom' }).first().click();
	await expect(chart).toBeVisible();
});

test('the date stepper moves the day; tomorrow is not offered', async ({ page }) => {
	await page.goto(day);
	const stepper = page.getByRole('group', { name: 'Day' });
	await expect(stepper).toContainText('Sep 14, 2026');
	await stepper.getByRole('button', { name: 'Previous day' }).click();
	await expect(page).toHaveURL(/end=2026-09-13/);
	await expect(page.getByRole('group', { name: /^Heart rate on .*Sep 13, 2026, 1-minute/ })).toBeVisible();
	await page.goto('/explore/heart_rate?range=1D');
	await expect(page.getByRole('group', { name: 'Day' }).getByRole('button', { name: 'Next day' })).toBeDisabled();
});

test('steps are 30-minute bars that zoom to 5-minute ones', async ({ page }) => {
	await page.goto('/explore/steps?range=1D&end=2026-09-14');
	const chart = page.getByRole('group', { name: /^Steps on .*Sep 14, 2026, 30-minute buckets/ });
	await expect(chart.locator('rect.bar')).toHaveCount(48);
	await drag(page, chart, 0.25, 0.25 + 1 / 24);
	await expect(page.getByRole('group', { name: /^Steps on .*Sep 14, 2026, 5-minute buckets/ }).locator('rect.bar')).not.toHaveCount(0);
});

test('a day on a longer range drills into the Day view; metrics without intraday have none', async ({ page }) => {
	await page.goto('/explore/steps?range=1W&end=2026-09-16');
	const chart = page.getByRole('group', { name: 'Steps, resolved per day' });
	const box = (await chart.boundingBox())!;
	await page.mouse.click(box.x + box.width - 40, box.y + box.height / 2);
	await chart.getByRole('button', { name: 'Day view' }).click();
	await expect(page).toHaveURL(/range=1D&end=2026-09-16/);
	await expect(page.getByRole('group', { name: /^Steps on .*Sep 16, 2026, 30-minute/ })).toBeVisible();

	await page.goto('/explore/resting_heart_rate?range=1D&end=2026-09-16');
	await expect(page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '1D' })).toHaveCount(0);
	await expect(page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '3M' })).toHaveAttribute('aria-pressed', 'true');
});

test('the all-sources day view reuses the Day chart', async ({ page }) => {
	await page.goto('/explore/heart_rate/day/2026-09-14');
	await expect(page.getByRole('group', { name: /^Heart rate on .*Sep 14, 2026, 1-minute buckets/ })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Sources' })).toBeVisible();
});
