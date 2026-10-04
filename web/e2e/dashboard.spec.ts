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

const reauth = 'Withings needs reauthorization.';
const alerts = (page: Page) => page.getByRole('region', { name: 'Alerts' });
const dismissButton = (page: Page, text: string | RegExp) => alerts(page).getByRole('button', { name: typeof text === 'string' ? `Dismiss: ${text}` : text });

test('dismissing an alert hides it at once, saves its key and survives a reload', async ({ page, dash }) => {
	dash.putDelay = 400;
	await page.goto('/');
	await expect(alerts(page).getByText(reauth)).toBeVisible();
	const button = dismissButton(page, reauth);
	const box = await button.boundingBox();
	expect(box!.width).toBeGreaterThanOrEqual(44);
	expect(box!.height).toBeGreaterThanOrEqual(44);

	await button.click();
	// Optimistic: gone before the save answers; the other alert stays and the count appears.
	await expect(alerts(page).getByText(reauth)).toHaveCount(0);
	await expect(alerts(page).getByText(/The last backup is from/)).toBeVisible();
	await expect(alerts(page).getByRole('button', { name: '1 dismissed · Show' })).toBeVisible();
	await expect.poll(() => dash.puts.length).toBe(1);
	expect(dash.puts[0].dismissed).toEqual([`reauth:conn_${'b'.repeat(32)}:2026-09-20T10:00:00Z`]);
	expect(dash.puts[0].cards.length).toBeGreaterThan(0);

	// Dismissing only hides it here: Connections still shows the state.
	await page.reload();
	await expect(alerts(page).getByText(/The last backup is from/)).toBeVisible();
	await expect(alerts(page).getByText(reauth)).toHaveCount(0);
	await expect(alerts(page).getByRole('button', { name: '1 dismissed · Show' })).toBeVisible();
});

test('Show lists the dismissed alerts and Restore brings one back', async ({ page, dash }) => {
	await page.goto('/');
	await dismissButton(page, reauth).click();
	await dismissButton(page, /^Dismiss: The last backup/).click();
	await expect(alerts(page).getByText('Nothing needs your attention.')).toHaveCount(0);
	await expect(alerts(page).getByText('No open alerts.')).toBeVisible();
	await expect.poll(() => dash.puts.length).toBe(2);

	await alerts(page).getByRole('button', { name: '2 dismissed · Show' }).click();
	await expect(alerts(page).getByText(reauth)).toBeVisible();
	await expect(alerts(page).getByText(/The last backup is from/)).toBeVisible();
	await expect(alerts(page).getByRole('button', { name: '2 dismissed · Hide' })).toBeVisible();

	await alerts(page).getByRole('button', { name: `Restore: ${reauth}` }).click();
	await expect.poll(() => dash.puts.length).toBe(3);
	expect(dash.puts[2].dismissed).toHaveLength(1);
	await expect(alerts(page).getByRole('button', { name: `Dismiss: ${reauth}` })).toBeVisible();
	await expect(alerts(page).getByRole('button', { name: '1 dismissed · Hide' })).toBeVisible();
	await alerts(page).getByRole('button', { name: '1 dismissed · Hide' }).click();
	await expect(alerts(page).getByText(/The last backup is from/)).toHaveCount(0);
	await page.reload();
	await expect(alerts(page).getByText(reauth)).toBeVisible();
	await expect(alerts(page).getByRole('button', { name: '1 dismissed · Show' })).toBeVisible();
});

test('a new occurrence of the problem shows again, and the old key is pruned on the next save', async ({ page, dash }) => {
	await page.goto('/');
	await dismissButton(page, reauth).click();
	await expect.poll(() => dash.puts.length).toBe(1);

	// The connection recovered and needs reauthorization again: a later success dates it anew.
	dash.reauthSince = '2026-09-28T10:00:00Z';
	await page.reload();
	await expect(alerts(page).getByText(reauth)).toBeVisible();
	await expect(alerts(page).getByRole('button', { name: /dismissed/ })).toHaveCount(0); // the old key matches nothing

	// The old key's alert is gone, so dismissing the backup alert saves without it.
	await dismissButton(page, /^Dismiss: The last backup/).click();
	await expect.poll(() => dash.puts.length).toBe(2);
	expect(dash.puts[1].dismissed).toEqual([expect.stringMatching(/^backup:/)]);
});

test('a failed save brings the alert back with the problem', async ({ page, dash }) => {
	dash.putStatus = 503;
	await page.goto('/');
	await dismissButton(page, reauth).click();
	await expect(alerts(page).getByText(reauth)).toBeVisible();
	await expect(page.getByRole('alert')).toBeVisible();
});

test('the layout saved in edit mode keeps the dismissed alerts', async ({ page, dash }) => {
	dash.dismissed = ['reauth:keep'];
	await page.goto('/');
	await page.getByRole('button', { name: 'Customize' }).click();
	await page.getByRole('button', { name: 'Save' }).click();
	await expect.poll(() => dash.puts.length).toBe(1);
	expect(dash.puts[0].dismissed).toEqual(['reauth:keep']);
});

test('no serious axe violations with dismissed alerts shown, light and dark', async ({ page, dash }) => {
	await page.goto('/');
	await dismissButton(page, reauth).click();
	await expect.poll(() => dash.puts.length).toBe(1);
	for (const showing of [false, true]) {
		if (showing) await alerts(page).getByRole('button', { name: '1 dismissed · Show' }).click();
		for (const scheme of ['light', 'dark'] as const) {
			await page.emulateMedia({ colorScheme: scheme });
			const { violations } = await new AxeBuilder({ page }).include('[aria-label="Alerts"]').analyze();
			const blocking = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
			expect(blocking.map((v) => `alerts shown=${showing} [${scheme}]: ${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`)).toEqual([]);
		}
	}
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

const tile = (page: Page, name: string) => page.getByRole('region', { name: 'Highlights' }).getByRole('button', { name: new RegExp(name) });
const hero = (page: Page) => page.locator('section.hero');

test('hero tiles drive the hero chart; the period switch reloads it, 1Y as weekly means', async ({ page, dash }) => {
	await page.goto('/');
	const tiles = page.getByRole('region', { name: 'Highlights' }).getByRole('button');
	await expect(tiles).toHaveCount(4);
	await expect(tiles.nth(0)).toContainText('Steps');
	await expect(tile(page, 'Steps')).toHaveAttribute('aria-pressed', 'true');
	await expect(tile(page, 'Steps')).toContainText(/30-day mean [\d,.]+/);
	await expect(hero(page).getByRole('heading', { level: 2 })).toHaveText('Steps');
	await expect(hero(page).getByText(/^[+−±][\d,.]+ vs previous 30 days$/)).toBeVisible();
	await expect(hero(page).getByText('Apple Health')).toBeVisible();
	await expect(hero(page).getByRole('link', { name: /Open in Explore/ })).toHaveAttribute('href', '/explore/steps');
	expect(dash.charts.at(-1)).toMatch(/^series metric=steps&.*window=local_day/);

	// Another tile takes over the chart: a line for a daily summary, with its gaps.
	await tile(page, 'Resting heart rate').click();
	await expect(tile(page, 'Resting heart rate')).toHaveAttribute('aria-pressed', 'true');
	await expect(tile(page, 'Steps')).toHaveAttribute('aria-pressed', 'false');
	await expect(hero(page).getByRole('heading', { level: 2 })).toHaveText('Resting heart rate');
	await expect(hero(page).locator('.hole').first()).toBeAttached();
	await expect(hero(page).getByText('WHOOP')).toBeVisible();

	// Periods: the tiles' means and the delta follow; 1Y reads weekly means from the trend.
	const period = hero(page).getByRole('group', { name: 'Period' });
	await period.getByRole('button', { name: '7D' }).click();
	await expect(period.getByRole('button', { name: '7D' })).toHaveAttribute('aria-pressed', 'true');
	await expect(tile(page, 'Steps')).toContainText(/7-day mean/);
	await expect(hero(page).getByText(/vs previous 7 days$/)).toBeVisible();
	await period.getByRole('button', { name: '1Y' }).click();
	await expect(hero(page).getByText('Weekly means of daily values')).toBeVisible();
	await expect(hero(page).getByText(/vs previous year$/)).toBeVisible();
	await expect(tile(page, 'Weight')).toContainText(/1-year mean/);
	expect(dash.charts.at(-1)).toMatch(/^trend metric=resting_heart_rate&.*grain=week/);
});

test('the hero tooltip: value, status and source; a click pins the day action', async ({ page }) => {
	await page.goto('/');
	await tile(page, 'Weight').click();
	const chart = hero(page).getByRole('group', { name: /^Weight, daily values of the last 30 days/ });
	await chart.focus();
	const tip = hero(page).locator('.tip');
	await expect(tip).toContainText('kg');
	await expect(tip).toContainText('Direct');
	await expect(tip).toContainText('Withings');
	await page.keyboard.press('Tab');
	await expect(tip).toBeHidden();
	// Bars (steps) show the providers too; a click pins the card with its action.
	await tile(page, 'Steps').click();
	const bars = hero(page).getByRole('group', { name: /^Steps, daily values/ });
	await expect(bars.locator('rect.bar').first()).toBeVisible();
	await bars.scrollIntoViewIfNeeded();
	const box = (await bars.boundingBox())!;
	await page.mouse.click(box.x + box.width - 30, box.y + box.height / 2);
	await expect(tip).toContainText('Apple Health');
	await tip.getByRole('button', { name: 'All sources of this day' }).click();
	await expect(page).toHaveURL(/\/explore\/steps\/day\/\d{4}-\d{2}-\d{2}$/);
});

test('last night: time asleep, bed and wake, stages and the source', async ({ page }) => {
	await page.goto('/');
	const night = page.getByRole('region', { name: 'Last night' });
	await expect(night.getByText(/^\d+h \d{2}m/).first()).toBeVisible();
	await expect(night.getByText(/(23:10|11:10).*(06:40|6:40)/)).toBeVisible();
	await expect(night.getByText('WHOOP')).toBeVisible();
	await expect(night.getByRole('group', { name: /Sleep stages of last night/ })).toBeVisible();
	await expect(night.getByRole('img', { name: 'Time in each sleep stage last night' })).toBeVisible();
	await expect(night.getByText(/^30-night mean \d+h \d{2}m$/)).toBeVisible();
	await expect(night.getByRole('link', { name: /Sleep view/ })).toHaveAttribute('href', '/explore/sleep');
});

test('customize the hero tiles: up to four, saved with the layout', async ({ page, dash }) => {
	await page.goto('/');
	await page.getByRole('button', { name: 'Customize' }).click();
	const pick = page.getByRole('group', { name: /Hero tiles/ });
	await expect(pick.getByRole('checkbox', { name: 'Steps' })).toBeChecked();
	// Four are chosen: the others wait until one is removed. Families are not offered.
	await expect(pick.getByRole('checkbox', { name: 'SpO₂' })).toBeDisabled();
	await expect(pick.getByRole('checkbox', { name: 'Sleep' })).toHaveCount(0);
	await pick.getByRole('checkbox', { name: 'Weight' }).uncheck();
	await pick.getByRole('checkbox', { name: 'SpO₂' }).check();
	await expect(tile(page, 'SpO₂')).toBeVisible();
	await expect(tile(page, 'Weight')).toHaveCount(0);
	await page.getByRole('button', { name: 'Save' }).click();
	await expect.poll(() => dash.puts.length).toBe(1);
	expect(dash.puts[0].hero).toEqual(['steps', 'resting_heart_rate', 'hrv_rmssd_nightly', 'spo2']);
	await page.reload();
	await expect(page.getByRole('region', { name: 'Highlights' }).getByRole('button').nth(3)).toContainText('SpO₂');
});

test('on a phone the tiles scroll sideways and the cards stack', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/');
	const strip = page.getByRole('region', { name: 'Highlights' });
	await expect(tile(page, 'Steps')).toBeVisible();
	const scroll = await strip.evaluate((el) => ({ sw: el.scrollWidth, cw: el.clientWidth }));
	expect(scroll.sw).toBeGreaterThan(scroll.cw);
	const [a, b] = await Promise.all([card(page, 'VO₂ max').boundingBox(), card(page, 'Weight').boundingBox()]);
	expect(b!.y).toBeGreaterThan(a!.y + a!.height - 1);
	expect(Math.abs(a!.x - b!.x)).toBeLessThan(1);
	const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
	expect(overflow).toBeLessThanOrEqual(0);
});
