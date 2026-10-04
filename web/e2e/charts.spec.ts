// The chart kit on the metric detail page (J23.4): TimeSeries with source overlays and the
// crosshair card, Bars, Histogram, BrushNavigator and CoverageStrip, in both themes, from the
// keyboard and as tables. Synthetic data (explore-fake.ts).
import { paint, schemes, walk } from './charts';
import { expect, test } from './explore-fake';

const detail = '/explore/resting_heart_rate?range=1M&end=2026-09-16';

for (const scheme of schemes) {
	test(`TimeSeries, tooltip, navigator, histogram and coverage (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto(detail);
		const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
		await expect(chart.locator('path.line')).toHaveCount(1);
		await page.getByLabel('Show sources').check();
		await expect(chart.locator('path.line')).toHaveCount(3); // resolved + one dashed overlay per source
		await expect(chart.locator('path.line.secondary').first()).toHaveAttribute('stroke-dasharray', /\d/);
		await expect(chart.locator('.marker.fallback')).toHaveCount(1); // status marker of the fallback day

		await walk(page, chart, { rows: 30 });

		// The card: date, value with its status, and every source at that point.
		await chart.focus();
		const tip = chart.locator('.tip');
		for (let i = 0; i < 2; i++) await page.keyboard.press('ArrowLeft');
		await expect(tip).toContainText('Sep 14, 2026');
		await expect(tip).toContainText('52bpm');
		await expect(tip).toContainText('Fallback');
		await expect(tip).toContainText('Garmin · watch');

		// A click pins the card and offers the point's actions; Raw records opens the day.
		const box = await chart.boundingBox();
		if (!box) throw new Error('no chart box');
		await page.mouse.move(box.x + box.width - 13, box.y + box.height / 2);
		await expect(tip).toBeVisible();
		await page.mouse.down();
		await page.mouse.up();
		await expect(tip.getByRole('button', { name: 'Explain' })).toBeVisible();
		await expect(tip.getByRole('button', { name: 'Override' })).toBeVisible();
		await tip.getByRole('button', { name: 'Explain' }).click();
		await expect(page.getByRole('region', { name: /Sep 16, 2026/ })).toBeVisible();

		// The navigator under the chart zooms it from the keyboard; Escape shows everything again.
		const nav = page.getByRole('group', { name: /Resting heart rate range: everything/ });
		await nav.focus();
		await page.keyboard.press('+');
		await expect(page.getByRole('group', { name: /Resting heart rate range: .* to / })).toBeVisible();
		await expect(page.getByRole('button', { name: 'Reset zoom' })).toBeVisible();
		await page.keyboard.press('Escape');
		await expect(page.getByRole('button', { name: 'Reset zoom' })).toBeHidden();

		// Distribution: counts per bin with the mean marked, also a table.
		const hist = page.getByRole('group', { name: /distribution in range/ });
		await expect(hist.locator('.mean')).toHaveCount(1);
		await walk(page, hist);

		await expect(page.getByRole('img', { name: /^garmin: data on/ })).toBeVisible();
	});

	test(`Bars (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/steps?range=1W&end=2026-09-16');
		const chart = page.getByRole('group', { name: 'Steps, resolved per day' });
		await expect(chart.locator('rect.bar')).toHaveCount(7);
		await walk(page, chart, { rows: 7 });
	});
}

test('the Raw records action opens the all-sources day', async ({ page }) => {
	await page.goto(detail);
	const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
	const box = await chart.boundingBox();
	if (!box) throw new Error('no chart box');
	await page.mouse.click(box.x + box.width - 13, box.y + box.height / 2);
	await chart.locator('.tip').getByRole('button', { name: 'Raw records' }).click();
	await expect(page).toHaveURL(/\/explore\/resting_heart_rate\/day\/2026-09-16$/);
});

test('the marks follow the theme', async ({ page }) => {
	await page.emulateMedia({ colorScheme: 'dark' });
	await page.goto(detail);
	const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
	const dark = [await paint(chart, 'path.line', 'stroke'), await paint(chart, '.grid', 'stroke')];
	await page.emulateMedia({ colorScheme: 'light' });
	const light = [await paint(chart, 'path.line', 'stroke'), await paint(chart, '.grid', 'stroke')];
	expect(dark[0]).not.toEqual(light[0]);
	expect(dark[1]).not.toEqual(light[1]);
});

test('touch scrubs through the points and a tap pins the card', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto(detail);
	const chart = page.getByRole('group', { name: /Resting heart rate, resolved per day/ });
	const box = await chart.boundingBox();
	if (!box) throw new Error('no chart box');
	const y = box.y + box.height / 2;
	const touch = (type: string, x: number) => chart.dispatchEvent(type, { pointerType: 'touch', pointerId: 7, isPrimary: true, button: 0, clientX: x, clientY: y });
	const tip = chart.locator('.tip');
	await touch('pointerdown', box.x + 60);
	const start = await tip.innerText();
	await touch('pointermove', box.x + box.width - 13);
	await expect(tip).not.toHaveText(start);
	await touch('pointerup', box.x + box.width - 13);
	await expect(tip).toBeVisible(); // a scrub leaves the card where it ended
	await touch('pointerdown', box.x + box.width - 13);
	await touch('pointerup', box.x + box.width - 13);
	await expect(tip.getByRole('button', { name: 'Explain' })).toBeVisible();
});
