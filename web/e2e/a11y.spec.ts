// axe-core over every section page, against the same fakes the functional specs use. The
// gate (E11 acceptance): no serious or critical violation. Moderate and minor findings are
// listed in the report but do not fail. Run with the rest: npx playwright test.
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { test as connTest, expect, ids, sidecarSecret } from './connections-fake';
import { test as dataTest, fallbackDay } from './explore-fake';
import { test as devicesTest } from './devices-fake';
import { test as labTest, syntheticPdf } from './lab-fake';
import { test as rulesTest } from './rules-fake';
import { test as settingsTest } from './settings-fake';
import { test as viewsTest } from './views-fake';
import { test as anonTest } from './fake-api';

/** Waits for the page to settle, then fails on serious or critical axe violations (light and dark). */
async function scan(page: Page, what: string) {
	await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
	await page.waitForLoadState('networkidle');
	for (const scheme of ['light', 'dark'] as const) {
		await page.emulateMedia({ colorScheme: scheme });
		const { violations } = await new AxeBuilder({ page }).analyze();
		const blocking = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
		expect(
			blocking.map((v) => `${what} [${scheme}]: ${v.id} (${v.impact}) ${v.help}\n  ${v.nodes.map((n) => n.target.join(' ')).join('\n  ')}`),
			'serious or critical axe violations'
		).toEqual([]);
	}
	await page.emulateMedia({ colorScheme: null });
}

anonTest('login', async ({ page }) => {
	await page.goto('/login');
	await scan(page, '/login');
});

connTest('Dashboard and connections', async ({ page }) => {
	for (const path of ['/', '/connections', `/connections/${ids.ultrahuman}`, `/connections/${ids.ultrahuman}?tab=backfills`, `/connections/${ids.ultrahuman}?tab=devices`, `/connections/${ids.withings}?tab=settings`]) {
		await page.goto(path);
		await scan(page, path);
	}
});

connTest('Connect wizard: provider list, prompt step, sidecar connection', async ({ page }) => {
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const wizard = page.getByRole('dialog', { name: 'Connect a source' });
	await expect(wizard.getByRole('radio', { name: /Offline sidecar/ })).toBeDisabled();
	await scan(page, '/connections (connect dialog)');
	await wizard.getByRole('radio', { name: /Example Collector/ }).check();
	await wizard.getByRole('button', { name: 'Continue to Example Collector' }).click();
	await expect(wizard.getByLabel('Password')).toBeVisible();
	await scan(page, '/connections (prompt step)');
	await wizard.getByLabel('Username').fill(sidecarSecret.username);
	await wizard.getByLabel('Password').fill(sidecarSecret.password);
	await wizard.getByRole('button', { name: 'Continue' }).click();
	await wizard.getByLabel('Verification code').fill(sidecarSecret.code);
	await wizard.getByRole('button', { name: 'Continue' }).click();
	await expect(page.getByRole('link', { name: 'example-collector' })).toBeVisible();
	await scan(page, '/connections/[id] (sidecar)');
});

dataTest('Explore: inventory, metric detail, day view', async ({ page }) => {
	dataTest.slow(); // many full-page scans in two themes and two widths
	const day = `/explore/resting_heart_rate/day/${fallbackDay}`;
	for (const path of ['/explore', '/explore/resting_heart_rate?range=1M&end=2026-09-16', '/explore/steps?range=1W&end=2026-09-16', day]) {
		await page.goto(path);
		await scan(page, path);
	}
	// The metric page with its overlays, the previous period and the rule lens, wide and at 390 px.
	for (const width of [1280, 390]) {
		await page.setViewportSize({ width, height: 900 });
		await page.goto('/explore/resting_heart_rate?range=1M&end=2026-09-16');
		await page.getByRole('group', { name: 'Series' }).getByRole('button', { name: 'Garmin' }).click();
		await page.getByRole('button', { name: 'Compare previous' }).click();
		await page.getByRole('button', { name: 'How it’s calculated' }).click();
		await expect(page.getByRole('complementary', { name: 'How this is calculated' })).toBeVisible();
		await scan(page, `/explore/[metric] (overlays and lens, ${width} px)`);
	}
	// The override dialog is part of the drilldown.
	await page.goto(day);
	await page.getByRole('button', { name: 'Set a value…' }).click();
	await expect(page.getByRole('dialog')).toBeVisible();
	await scan(page, `${day} (override dialog)`);
});

rulesTest('Rules: catalogue, metric page, builder', async ({ page, rules }) => {
	rules.preview = 'ok';
	for (const path of ['/rules', '/rules/new?metric=heart_rate', '/rules/heart_rate']) {
		await page.goto(path);
		await scan(page, path);
	}
	// Steps 2 to 5 of the builder, from the rule in effect (step 1 is the page scanned above).
	await page.goto('/rules/new?metric=heart_rate');
	for (let step = 2; step <= 5; step++) {
		if (step > 2) await page.getByRole('button', { name: 'Next' }).click();
		await expect(page.getByRole('heading', { name: new RegExp(`^${step}\\.`) })).toBeVisible();
		await scan(page, `/rules/new step ${step}`);
	}
	await expect(page.getByRole('status').filter({ hasText: 'days change' })).toBeVisible();
});

labTest('Lab results: list, review, results', async ({ page }) => {
	await page.goto('/lab');
	await scan(page, '/lab (empty)');
	await page.getByLabel('Choose a PDF').setInputFiles({ name: 'synthetic-report.pdf', mimeType: 'application/pdf', buffer: syntheticPdf() });
	const docRow = page.getByRole('row', { name: /synthetic-report\.pdf/ });
	await expect(docRow).toBeVisible();
	await scan(page, '/lab (with a document)');

	await docRow.getByRole('button', { name: 'Extract' }).click();
	const dialog = page.getByRole('dialog', { name: 'Extract results' });
	await scan(page, '/lab (extract dialog)');
	await dialog.getByRole('button', { name: 'Extract', exact: true }).click();
	await expect(page).toHaveURL(/\/lab\/documents\/doc_/);
	await expect(page.getByRole('table', { name: 'Extracted rows' }).getByRole('row')).toHaveCount(5);
	await expect(page.getByRole('img', { name: 'Page 1 of the PDF' })).toBeVisible();
	await scan(page, '/lab/documents/[id]');

	await page.goto('/lab/results');
	await scan(page, '/lab/results');
});

settingsTest('Settings pages', async ({ page }) => {
	for (const path of ['/settings', '/settings/api-keys', '/settings/ai', '/settings/backups', '/settings/retention', '/settings/security', '/settings/system']) {
		await page.goto(path);
		await scan(page, path);
	}
});

devicesTest('Settings › Devices: pairing code, devices, origins, resync dialog', async ({ page }) => {
	await page.goto('/settings/devices');
	await expect(page.getByRole('row', { name: /Synthetic iPhone/ })).toBeVisible();
	await scan(page, '/settings/devices');
	await page.getByRole('button', { name: 'Create pairing code' }).click();
	await expect(page.getByRole('img', { name: /Pairing QR code/ })).toBeVisible();
	await scan(page, '/settings/devices (pairing code)');
	await page.getByRole('button', { name: 'Resync Synthetic iPhone' }).click();
	await expect(page.getByRole('dialog')).toBeVisible();
	await scan(page, '/settings/devices (resync dialog)');
});

viewsTest('Specialised views: sleep, blood pressure, body composition, workouts, events, lab analyte', async ({ page }) => {
	viewsTest.slow(); // many full-page scans in two themes and two widths
	for (const path of ['/explore/sleep', '/explore/blood-pressure', '/explore/body-composition', '/explore/workouts', '/explore/events', '/lab/analytes/glucose']) {
		await page.goto(path);
		await scan(page, path);
	}
	// A phone (390 px), with a night of the list opened.
	await page.setViewportSize({ width: 390, height: 844 });
	for (const path of ['/explore/sleep', '/explore/blood-pressure', '/explore/body-composition', '/explore/workouts', '/explore/events']) {
		await page.goto(path);
		if (path.endsWith('sleep')) await page.getByRole('region', { name: 'Nights' }).locator('summary').first().click();
		await scan(page, `${path} (390 px)`);
	}
});
