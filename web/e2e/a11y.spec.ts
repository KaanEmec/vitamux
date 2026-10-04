// axe-core over every section page, against the same fakes the functional specs use. The
// gate (E11 acceptance): no serious or critical violation. Moderate and minor findings are
// listed in the report but do not fail. Run with the rest: npx playwright test.
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { test as connTest, expect, ids, sidecarSecret } from './connections-fake';
import { test as dataTest, fallbackDay } from './data-fake';
import { test as labTest, syntheticPdf } from './lab-fake';
import { test as rulesTest } from './rules-fake';
import { test as settingsTest } from './settings-fake';
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

connTest('Today and connections', async ({ page }) => {
	for (const path of ['/', '/connections', `/connections/${ids.ultrahuman}`, `/connections/${ids.ultrahuman}?tab=backfills`, `/connections/${ids.withings}?tab=settings`]) {
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
	await wizard.getByRole('radio', { name: /Example sidecar/ }).check();
	await wizard.getByRole('button', { name: 'Continue to Example sidecar' }).click();
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

dataTest('Data: daily view, drilldown, sleep, workouts', async ({ page }) => {
	const day = `/data/day/resting_heart_rate/${fallbackDay}`;
	for (const path of [
		'/data?metric=resting_heart_rate&start=2026-09-12&end=2026-09-16',
		day,
		`/data/sleep?date=${fallbackDay}`,
		'/data/workouts?start=2026-09-12&end=2026-09-16'
	]) {
		await page.goto(path);
		await scan(page, path);
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
