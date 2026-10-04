import { expect, syntheticPdf, test } from './lab-fake';

// The review's keyboard flow (J and K move between rows, E edits, Enter accepts, Escape leaves a
// field); typing in a field never triggers a shortcut.
test('review by keyboard: J and K move, E edits, Enter accepts, fields keep their own keys', async ({ page, lab }) => {
	await page.goto('/lab');
	await page.getByLabel('Choose a PDF').setInputFiles({ name: 'synthetic-report.pdf', mimeType: 'application/pdf', buffer: syntheticPdf() });
	await page.getByRole('row', { name: /synthetic-report\.pdf/ }).getByRole('button', { name: 'Extract' }).click();
	await page.getByRole('dialog', { name: 'Extract results' }).getByRole('button', { name: 'Extract', exact: true }).click();

	const rows = page.getByRole('table', { name: 'Extracted rows' });
	await expect(rows.getByRole('row')).toHaveCount(5);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Review extracted rows');
	const current = rows.locator('tr[aria-current="true"]');
	await expect(current).toHaveCount(0);

	// J selects the first row, then walks down; K walks back and stops at the top.
	await page.keyboard.press('j');
	await expect(current).toContainText('Glucose');
	await expect(page.getByRole('form', { name: 'Row 1: Glucose' })).toBeVisible();
	await page.keyboard.press('j');
	await expect(current).toContainText('Creatinine');
	await page.keyboard.press('k');
	await page.keyboard.press('k');
	await expect(current).toContainText('Glucose');

	// E selects the printed value for retyping; keys typed there are text, not shortcuts.
	await page.keyboard.press('e');
	const value = page.getByLabel('Value as printed');
	await expect(value).toBeFocused();
	await page.keyboard.type('jk');
	await expect(value).toHaveValue('jk');
	await expect(current).toContainText('Glucose');
	await page.keyboard.press('Escape');
	await expect(value).not.toBeFocused();
	await page.getByRole('button', { name: 'Undo changes' }).click();
	await expect(value).toHaveValue('5.1');

	// Enter accepts the row as read and review moves on to the next one waiting.
	await page.keyboard.press('Enter');
	await expect(rows.getByRole('row', { name: /Glucose/ })).toContainText('Accepted');
	await expect(current).toContainText('Creatinine');
	await expect(page.getByText('1 of 4 reviewed')).toBeVisible();
	await page.keyboard.press('Enter');
	await expect(rows.getByRole('row', { name: /Creatinine/ })).toContainText('Accepted');
	await expect(current).toContainText('#######');
	expect(lab.rowPatches).toEqual([{ review: 'accept' }, { review: 'accept' }]);

	// A focused button keeps Enter for itself: it activates once, not twice.
	await page.getByRole('button', { name: 'Reject row' }).focus();
	await page.keyboard.press('Enter');
	await expect(rows.getByRole('row', { name: /#######/ })).toContainText('Rejected');
	expect(lab.rowPatches).toEqual([{ review: 'accept' }, { review: 'accept' }, { review: 'reject' }]);
	await expect(page.getByRole('progressbar', { name: 'Rows reviewed' })).toHaveAttribute('aria-valuenow', '3');
});
