// Shared checks for the chart kit specs (J23.4): every x/y chart is one tab stop whose keys move a
// tooltip through its points, and has a table fallback. Charts are drawn in both themes.
import { expect, type Locator, type Page } from '@playwright/test';

export const schemes = ['dark', 'light'] as const;

/** Focus the chart, walk it with the keys and open its table; returns the tooltip texts seen. */
export async function walk(page: Page, chart: Locator, opts: { rows?: number } = {}) {
	await chart.focus();
	await expect(chart).toBeFocused();
	const tip = chart.locator('.tip');
	await expect(tip).toBeVisible(); // focus shows the last point
	const last = await tip.innerText();
	await page.keyboard.press('Home');
	const first = await tip.innerText();
	expect(first).not.toEqual(last);
	await page.keyboard.press('PageDown');
	await page.keyboard.press('ArrowRight');
	await page.keyboard.press('End');
	await expect(tip).toHaveText(last);
	// The live region announces the point in words.
	await expect(chart.locator('..').locator('[aria-live="polite"]')).not.toBeEmpty();
	// One tab stop: Tab leaves the chart.
	await page.keyboard.press('Tab');
	await expect(chart).not.toBeFocused();
	await expect(tip).toBeHidden();

	const frame = chart.locator('..');
	await frame.getByText('Show as a table').click();
	const table = frame.getByRole('table');
	await expect(table).toBeVisible();
	if (opts.rows != null) await expect(table.locator('tbody tr')).toHaveCount(opts.rows);
	else expect(await table.locator('tbody tr').count()).toBeGreaterThan(0);
	return { first, last };
}

/** The computed paint of the first element matching `selector` in the chart. */
export const paint = (chart: Locator, selector: string, prop: 'fill' | 'stroke') =>
	chart.locator(selector).first().evaluate((el, p) => getComputedStyle(el).getPropertyValue(p), prop);
