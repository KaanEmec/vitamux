import { expect, test } from './fake-api';

test.beforeEach(({ api }) => {
	api.signedIn = true;
});

test('the shell is keyboard navigable', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');

	// First Tab reaches the skip link, then the brand, then the sections in order.
	await page.keyboard.press('Tab');
	await expect(page.getByRole('link', { name: 'Skip to content' })).toBeFocused();
	await page.keyboard.press('Tab');
	await expect(page.getByRole('link', { name: 'Vitamux' })).toBeFocused();

	const nav = page.getByRole('navigation', { name: 'Sections' });
	for (const name of ['Dashboard', 'Explore', 'Connections', 'Rules', 'Lab results', 'Settings']) {
		await page.keyboard.press('Tab');
		await expect(nav.getByRole('link', { name })).toBeFocused();
	}
	await page.keyboard.press('Enter');
	await expect(page).toHaveURL('/settings');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Settings');
	await expect(nav.getByRole('link', { name: 'Settings' })).toHaveAttribute('aria-current', 'page');
	await expect(nav.getByRole('link', { name: 'Dashboard' })).not.toHaveAttribute('aria-current');

	await page.keyboard.press('Tab');
	await page.keyboard.press('Enter'); // skip link after navigation focus reset
	await expect(page.locator('main')).toBeFocused();
});

test('every section has a route', async ({ page }) => {
	for (const [path, heading] of [
		['/', 'Dashboard'],
		['/explore', 'Explore'],
		['/connections', 'Connections'],
		['/data', 'Data'],
		['/rules', 'Rules'],
		['/lab', 'Lab results'],
		['/settings', 'Settings']
	]) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
		await expect(page).toHaveTitle(`${heading} · Vitamux`);
	}
});
