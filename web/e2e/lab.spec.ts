import type { Page } from '@playwright/test';
import { expect, geminiModel, syntheticPdf, test } from './lab-fake';

async function uploadReport(page: Page, name = 'synthetic-report.pdf') {
	await page.getByLabel('Choose a PDF').setInputFiles({ name, mimeType: 'application/pdf', buffer: syntheticPdf() });
	await expect(page.getByText(`Stored ${name}.`)).toBeVisible();
}

test('upload, extract with the fake, review, confirm, revise, see history, delete keeping results', async ({ page, lab }) => {
	await page.goto('/lab');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Lab results');
	await expect(page.getByText('No documents yet.')).toBeVisible();

	// The server's refusal is shown in words, not as a code.
	await page.getByLabel('Choose a PDF').setInputFiles({ name: 'notes.pdf', mimeType: 'application/pdf', buffer: Buffer.from('plain text') });
	await expect(page.getByRole('alert')).toContainText('This file is not a PDF.');

	await uploadReport(page);
	const docRow = page.getByRole('row', { name: /synthetic-report\.pdf/ });
	await expect(docRow).toContainText('Uploaded');

	// The fake extractor needs no consent.
	await docRow.getByRole('button', { name: 'Extract' }).click();
	const dialog = page.getByRole('dialog', { name: 'Extract results' });
	await expect(dialog.getByLabel(/Built-in test extractor/)).toBeChecked();
	await expect(dialog.getByText('Consent for this document')).toHaveCount(0);
	await dialog.getByRole('button', { name: 'Extract', exact: true }).click();
	await expect(page).toHaveURL(/\/lab\/documents\/doc_/);
	expect(lab.extractionBodies).toEqual([{ provider: 'fake', consent: null }]);

	// The run finishes on the next poll; the PDF renders beside the rows.
	const rows = page.getByRole('table', { name: 'Extracted rows' });
	await expect(rows.getByRole('row')).toHaveCount(5);
	await expect(page.getByText('4 of 4 rows not reviewed yet.')).toBeVisible();
	await expect(page.getByRole('img', { name: 'Page 1 of the PDF' })).toBeVisible();
	await expect(page.getByText('Page 1 of 1')).toBeVisible();

	// Confirming early lists what is missing.
	await page.getByRole('button', { name: 'Confirm results' }).click();
	const notReady = page.getByRole('alert').filter({ hasText: 'Not ready to confirm.' });
	await expect(notReady).toContainText('Row 1 (Glucose): not reviewed: accept, edit or reject it');
	await expect(notReady.getByRole('listitem')).toHaveCount(4);

	// Row 1: the evidence is outlined on the page; accept as read. Review moves to the next row.
	await notReady.getByRole('button', { name: 'Go to row 1' }).click();
	await expect(page.getByRole('img', { name: 'Page 1 of the PDF, selected row outlined' })).toBeVisible();
	await expect(page.locator('svg.overlay rect.highlight')).toHaveCount(1);
	// pdf.js drew the page: the canvas holds dark text pixels.
	const inked = () =>
		page.locator('canvas[data-page="1"]').evaluate((c: HTMLCanvasElement) => {
			const d = c.getContext('2d')!.getImageData(0, 0, c.width, c.height).data;
			let n = 0;
			for (let i = 0; i < d.length; i += 4) if (d[i] < 128 && d[i + 3] > 0) n++;
			return n;
		});
	await expect.poll(inked).toBeGreaterThan(200);
	const editor1 = page.getByRole('form', { name: 'Row 1: Glucose' });
	await expect(editor1.getByText('From a label alias match')).toBeVisible();
	await editor1.getByRole('button', { name: 'Accept as read' }).click();

	// Row 2 shows the printed flag verbatim; accept it.
	const editor2 = page.getByRole('form', { name: 'Row 2: Creatinine' });
	await expect(editor2.getByLabel('Flag as printed')).toHaveValue('H');
	await editor2.getByRole('button', { name: 'Accept as read' }).click();

	// Row 3 is unreadable: the checks state facts; reject it.
	const editor3 = page.getByRole('form', { name: 'Row 3: #######' });
	await expect(editor3.getByRole('list', { name: 'Checks for this row' })).toContainText('No analyte matches this label.');
	await expect(editor3).toContainText('The extractor could not read the value.');
	await editor3.getByRole('button', { name: 'Reject row' }).click();

	// Row 4 lacks its collection date: an invalid date comes back as a field error, then fix it.
	const editor4 = page.getByRole('form', { name: 'Row 4: HbA1c' });
	await expect(editor4).toContainText('No collection date was read.');
	await editor4.getByLabel('Collected').fill('30/09/2026');
	await editor4.getByRole('button', { name: 'Save and accept' }).click();
	await expect(editor4.getByText('must be an ISO 8601 local date')).toBeVisible();
	await editor4.getByLabel('Collected').fill('2026-09-30');
	await editor4.getByRole('button', { name: 'Save and accept' }).click();
	await expect(rows.getByRole('row', { name: /HbA1c/ })).toContainText('Edited');
	await expect(rows.getByRole('row', { name: /#######/ })).toContainText('Rejected');
	expect(lab.rowPatches).toEqual([
		{ review: 'accept' },
		{ review: 'accept' },
		{ review: 'reject' },
		{ collected_at: '30/09/2026', review: 'accept' },
		{ collected_at: '2026-09-30', review: 'accept' }
	]);

	await page.getByRole('button', { name: 'Confirm results' }).click();
	await expect(page.getByText('Confirmed 3 results.')).toBeVisible();

	// A later edit becomes a new revision when confirmed again.
	await rows.getByRole('button', { name: 'Review row 1: Glucose' }).click();
	const again = page.getByRole('form', { name: 'Row 1: Glucose' });
	await again.getByLabel('Value as printed').fill('5.2');
	await again.getByLabel('Numeric value').fill('5.2');
	await again.getByRole('button', { name: 'Save and accept' }).click();
	expect(lab.rowPatches.at(-1)).toEqual({ value_text: '5.2', value_numeric: 5.2, review: 'accept' });
	await page.getByRole('button', { name: 'Confirm again' }).click();
	await expect(page.getByText('Confirmed 3 results.')).toBeVisible();

	// Results by analyte, printed values, history.
	await page.getByRole('link', { name: 'See results' }).click();
	await expect(page).toHaveURL('/lab/results');
	const glucose = page.getByRole('table', { name: 'Results for glucose' });
	await expect(glucose).toContainText('5.2');
	await expect(glucose).toContainText('3.9 - 5.5');
	await expect(page.getByRole('table', { name: 'Results for creatinine' })).toContainText('umol/L');
	await expect(page.getByRole('table', { name: 'Results for hba1c' })).toContainText('2026-09-30');
	await expect(page.getByText('#######')).toHaveCount(0);
	await glucose.getByRole('button', { name: /History of Glucose/ }).click();
	const history = page.getByRole('dialog', { name: 'History of Glucose' });
	await expect(history.getByRole('row')).toHaveCount(3);
	await expect(history.getByRole('row', { name: /2 \(current\)/ })).toContainText('5.2');
	await expect(history.getByRole('row', { name: /^1 / })).toContainText('5.1');
	await history.getByRole('button', { name: 'Close' }).click();

	// Delete the document, keeping its results.
	await page.getByRole('link', { name: 'Documents', exact: true }).click();
	await page.getByRole('button', { name: 'Delete synthetic-report.pdf' }).click();
	const del = page.getByRole('dialog', { name: 'Delete document' });
	await expect(del.getByLabel(/Keep them/)).toBeChecked();
	await del.getByRole('button', { name: 'Delete document' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'its confirmed results are kept' })).toBeVisible();
	await expect(page.getByRole('row', { name: /Deleted/ })).toBeVisible();
	expect(lab.deletes).toEqual(['keep']);
	await page.getByRole('link', { name: 'Results', exact: true }).click();
	await expect(page.getByRole('table', { name: 'Results for glucose' })).toContainText('5.2');
});

test('an external extractor asks for consent naming provider and model', async ({ page, lab }) => {
	await page.goto('/lab');
	await uploadReport(page);
	await page.getByRole('row', { name: /synthetic-report\.pdf/ }).getByRole('button', { name: 'Extract' }).click();
	const dialog = page.getByRole('dialog', { name: 'Extract results' });
	await expect(dialog.getByLabel(/OpenAI/)).toBeDisabled();
	await dialog.getByLabel(/Google Gemini/).check();

	const consent = dialog.getByRole('group', { name: 'Consent for this document' });
	await expect(consent).toContainText('Google Gemini');
	await expect(consent).toContainText(geminiModel);
	const send = dialog.getByRole('button', { name: 'Send and extract' });
	await expect(send).toBeDisabled();
	await consent.getByLabel(`I consent to send this PDF to Google Gemini, model ${geminiModel}.`).check();
	await send.click();

	await expect(page).toHaveURL(/\/lab\/documents\/doc_/);
	const body = lab.extractionBodies[0] as { provider: string; consent: { provider: string; model: string; acknowledged_at: string } };
	expect(body.provider).toBe('gemini');
	expect(body.consent).toMatchObject({ provider: 'gemini', model: geminiModel });
	expect(Date.parse(body.consent.acknowledged_at)).not.toBeNaN();
	await expect(page.getByText('Read by Google Gemini (gemini-synthetic-model)')).toBeVisible();
});
