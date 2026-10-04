import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { daysAgo, defaultLayout, expect, test } from './dashboard-fake';

const card = (page: Page, name: string) => page.getByRole('article', { name, exact: true });
const titles = (page: Page) => page.locator('article h3').allTextContents();

test('the default layout: hero, cards, sources, delta, alerts and health', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');
	await expect(page).toHaveTitle('Dashboard · Vitamux');
	await expect(page.getByText('Resolved values for Europe/Amsterdam')).toBeVisible();

	// Cards in layout order; HRV (SDNN) has no data, so its card waits.
	await expect(card(page, 'Sleep')).toBeVisible();
	expect(await titles(page)).toEqual(
		['Sleep', 'Resting heart rate', 'HRV · nightly RMSSD', 'Steps', 'VO₂ max', 'Weight', 'Blood pressure', 'SpO₂', 'Respiratory rate', 'Active energy']
	);

	const sleep = card(page, 'Sleep');
	await expect(sleep.locator('.value')).toHaveText(/^\d+h \d{2}m\s*asleep$/);
	await expect(sleep.getByText(/^in bed \d+h \d{2}m$/)).toBeVisible();
	await expect(sleep.getByRole('img', { name: 'Time in each sleep stage' })).toBeVisible();
	await expect(sleep.getByText(/Deep\s+\d+h \d{2}m/)).toBeVisible();
	await expect(sleep.getByText(/^30-day mean \d+h \d{2}m$/)).toBeVisible();

	const rhr = card(page, 'Resting heart rate');
	await expect(rhr.getByText('bpm')).toBeVisible();
	await expect(rhr.getByText('Direct')).toBeVisible();
	await expect(rhr.getByText('WHOOP')).toBeVisible();
	await expect(rhr.getByText(/vs 30-day mean$/)).toBeVisible();
	await expect(card(page, 'SpO₂').getByText('Fallback')).toBeVisible();
	// Today's steps are not final: a status word, "so far" and the mean instead of a delta.
	const steps = card(page, 'Steps');
	await expect(steps.getByText('Partial')).toBeVisible();
	await expect(steps.getByText('so far')).toBeVisible();
	await expect(steps.getByText(/^30-day mean /)).toBeVisible();
	await expect(card(page, 'Blood pressure').locator('.value')).toHaveText(/^\d+\/\d+\s*mmHg$/);

	// A card opens its metric; the day's drilldown stays reachable.
	await expect(rhr.getByRole('link', { name: 'Resting heart rate', exact: true })).toHaveAttribute('href', '/explore/resting_heart_rate');
	await expect(rhr.getByRole('link', { name: /All sources/ })).toHaveAttribute('href', /^\/explore\/resting_heart_rate\/day\/\d{4}-\d{2}-\d{2}$/);

	const alerts = page.getByRole('region', { name: 'Alerts' });
	await expect(alerts.getByText('Withings needs reauthorization.')).toBeVisible();
	await expect(alerts.getByText(/The last backup is from/)).toBeVisible();
	const health = page.getByRole('region', { name: 'Connections' });
	await expect(health.getByRole('listitem').filter({ hasText: 'Apple Health' }).getByText('Healthy')).toBeVisible();
});

test('customize: reorder, resize, hide, add, save, and the layout survives a reload', async ({ page, dash }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Customize' }).click();
	const bar = page.getByRole('region', { name: 'Editing dashboard' });
	await expect(bar).toBeVisible();
	// The first card cannot move earlier, the last cannot move later.
	await expect(page.getByRole('button', { name: 'Move Sleep earlier' })).toBeDisabled();
	await expect(page.getByRole('button', { name: 'Move Active energy later' })).toBeDisabled();

	// Reorder with the keyboard buttons.
	await page.getByRole('button', { name: 'Move Weight earlier' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Weight is now card 6 of 11.' })).toBeAttached();
	expect((await titles(page)).slice(4, 8)).toEqual(['Steps', 'Weight', 'VO₂ max', 'Blood pressure']);

	// Resize and hide.
	const size = card(page, 'Respiratory rate').getByRole('group', { name: 'Size of Respiratory rate' });
	await size.getByRole('button', { name: 'L', exact: true }).click();
	await expect(size.getByRole('button', { name: 'L', exact: true })).toHaveAttribute('aria-pressed', 'true');
	await page.getByRole('button', { name: 'Hide SpO₂' }).click();
	await expect(card(page, 'SpO₂')).toHaveCount(0);
	await expect(page.getByRole('heading', { name: 'Hidden · 1' })).toBeVisible();

	// Add from the catalogue: blood pressure parts and sleep stages are one card each.
	await bar.getByRole('button', { name: 'Add metric' }).click();
	const drawer = page.getByRole('dialog', { name: 'Add metric' });
	await expect(drawer.getByRole('button', { name: 'Unpin Blood pressure' })).toBeVisible();
	await expect(drawer.getByRole('button', { name: 'Unpin Sleep' })).toBeVisible();
	await expect(drawer.getByRole('button', { name: /Bp |Sleep total|Sleep deep/ })).toHaveCount(0);
	await drawer.getByLabel('Search the catalogue').fill('fat');
	await drawer.getByRole('button', { name: 'Pin Body fat ratio' }).click();
	await expect(drawer.getByRole('button', { name: 'Unpin Body fat ratio' })).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(drawer).toBeHidden();
	await expect(card(page, 'Body fat ratio')).toBeVisible();

	// Cancel keeps the saved layout; nothing was sent yet.
	expect(dash.puts).toHaveLength(0);
	await bar.getByRole('button', { name: 'Save' }).click();
	await expect(bar).toBeHidden();
	expect(dash.puts).toHaveLength(1);
	const sent = dash.puts[0];
	expect(sent.version).toBe(1);
	expect(sent.cards.map((c) => c.metric).slice(4, 8)).toEqual(['steps', 'weight', 'vo2max', 'blood_pressure']);
	expect(sent.cards.find((c) => c.metric === 'respiratory_rate')?.size).toBe('L');
	expect(sent.cards.find((c) => c.metric === 'spo2')?.hidden).toBe(true);
	expect(sent.cards.at(-1)).toEqual({ metric: 'body_fat_ratio', size: 'S', hidden: false });

	// After a reload the server's layout is shown: order, sizes and the hidden card.
	await page.reload();
	await expect(card(page, 'Sleep')).toBeVisible();
	expect(await titles(page)).toEqual(
		['Sleep', 'Resting heart rate', 'HRV · nightly RMSSD', 'Steps', 'Weight', 'VO₂ max', 'Blood pressure', 'Respiratory rate', 'Active energy']
	);
	await expect(card(page, 'SpO₂')).toHaveCount(0);
});

test('drag a card to a new place', async ({ page, dash }) => {
	await page.setViewportSize({ width: 1280, height: 2400 }); // source and target on screen, as a drag needs
	await page.goto('/');
	await page.getByRole('button', { name: 'Customize' }).click();
	await page.getByRole('img', { name: 'Drag to reorder Weight' }).dragTo(card(page, 'Resting heart rate'));
	expect((await titles(page)).slice(0, 3)).toEqual(['Sleep', 'Weight', 'Resting heart rate']);
	await page.getByRole('button', { name: 'Save' }).click();
	await expect.poll(() => dash.puts.length).toBe(1);
	expect(dash.puts[0].cards.slice(0, 3).map((c) => c.metric)).toEqual(['sleep', 'weight', 'resting_heart_rate']);
});

test('cancel discards the edits; reset restores the curated default', async ({ page, dash }) => {
	dash.stored = defaultLayout.map((c) => (c.metric === 'steps' ? { ...c, hidden: true } : c));
	await page.goto('/');
	await expect(card(page, 'Sleep')).toBeVisible();
	await expect(card(page, 'Steps')).toHaveCount(0);

	await page.getByRole('button', { name: 'Customize' }).click();
	await page.getByRole('button', { name: 'Hide Weight' }).click();
	await page.getByRole('button', { name: 'Cancel' }).click();
	await expect(card(page, 'Weight')).toBeVisible();
	expect(dash.puts).toHaveLength(0);

	await page.getByRole('button', { name: 'Customize' }).click();
	await page.getByRole('button', { name: 'Reset to default' }).click();
	await expect(card(page, 'Steps')).toBeVisible();
	await page.getByRole('button', { name: 'Save' }).click();
	await expect.poll(() => dash.puts.length).toBe(1);
	expect(dash.puts[0].cards).toEqual(defaultLayout);
});

test('a past day: the date selector, final steps and back to today', async ({ page, dash }) => {
	const past = daysAgo(3);
	await page.goto(`/?date=${past}`);
	await expect(card(page, 'Steps')).toBeVisible();
	await expect(page.getByLabel('Date')).toHaveValue(past);
	await expect(card(page, 'Steps').getByText('Direct')).toBeVisible();
	await expect(card(page, 'Steps').getByText('so far')).toHaveCount(0);
	await expect(card(page, 'Steps').getByRole('link', { name: /All sources/ })).toHaveAttribute('href', `/explore/steps/day/${past}`);
	expect(dash.summaryDates.every((d) => d === past)).toBe(true);

	await page.getByRole('button', { name: 'Previous day' }).click();
	await expect(page).toHaveURL(`/?date=${daysAgo(4)}`);
	await expect(page.getByLabel('Date')).toHaveValue(daysAgo(4));
	await expect(card(page, 'Steps')).toBeVisible();

	await page.getByRole('button', { name: 'Today' }).click();
	await expect(page).toHaveURL('/');
	await expect(card(page, 'Steps').getByText('Partial')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Next day' })).toBeDisabled();
});

test('an empty install points to Connections', async ({ page, dash }) => {
	dash.empty = true;
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');
	await expect(page.getByText('No data yet')).toBeVisible();
	await expect(page.locator('article')).toHaveCount(0);
	await page.getByRole('main').getByRole('link', { name: 'Connect a source' }).first().click();
	await expect(page).toHaveURL('/connections');
});

test('the layout endpoint not being ready leaves the metrics empty', async ({ page, dash }) => {
	dash.layoutStatus = 503;
	await page.goto('/');
	await expect(page.getByText('Resolved values are not available yet.')).toBeVisible();
	await expect(page.getByRole('button', { name: 'Customize' })).toBeDisabled();
	await expect(page.getByRole('region', { name: 'Alerts' }).getByText('Withings needs reauthorization.')).toBeVisible();
});

test('no serious axe violations: view and edit mode, light and dark, desktop and phone', async ({ page }) => {
	const scan = async (what: string) => {
		await page.waitForLoadState('networkidle');
		for (const scheme of ['light', 'dark'] as const) {
			await page.emulateMedia({ colorScheme: scheme });
			const { violations } = await new AxeBuilder({ page }).analyze();
			const blocking = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
			expect(blocking.map((v) => `${what} [${scheme}]: ${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`)).toEqual([]);
		}
	};
	await page.goto('/');
	await expect(card(page, 'Sleep')).toBeVisible();
	await scan('view');
	await page.getByRole('button', { name: 'Customize' }).click();
	await scan('edit');
	await page.getByRole('button', { name: 'Add metric' }).click();
	await scan('add metric');
	await page.keyboard.press('Escape');
	await page.setViewportSize({ width: 375, height: 800 });
	await scan('edit, phone');
	await page.getByRole('button', { name: 'Cancel' }).click();
	await scan('view, phone');
	const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
	expect(overflow).toBeLessThanOrEqual(0);
});
