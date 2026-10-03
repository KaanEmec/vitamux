import { expect, owner, test } from './fake-api';
import type { Page } from '@playwright/test';

async function signIn(page: Page, password = owner.password) {
	await page.getByLabel('Username').fill(owner.username);
	await page.getByLabel('Password').fill(password);
	await page.getByRole('button', { name: 'Sign in' }).click();
}

test('signs in with a password and returns to the requested page', async ({ page }) => {
	await page.goto('/rules?metric=steps');
	await expect(page).toHaveURL('/login?next=%2Frules%3Fmetric%3Dsteps');

	await signIn(page, 'wrong-password');
	await expect(page.getByRole('alert')).toContainText('invalid username, password or code');

	await signIn(page);
	await expect(page).toHaveURL('/rules?metric=steps');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Rules');
	await expect(page.getByText(`Signed in as ${owner.username}`)).toBeVisible();
});

test('asks for a TOTP code when required', async ({ page, api }) => {
	api.totpEnabled = true;
	await page.goto('/login');
	await signIn(page);

	const code = page.getByLabel('Authenticator code');
	await expect(code).toBeFocused();
	await code.fill('000000');
	await page.getByRole('button', { name: 'Verify' }).click();
	await expect(page.getByRole('alert')).toContainText('invalid username, password or code');

	await code.fill(owner.totp);
	await page.getByRole('button', { name: 'Verify' }).click();
	await expect(page).toHaveURL('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
});

test('accepts a recovery code instead of TOTP', async ({ page, api }) => {
	api.totpEnabled = true;
	await page.goto('/login');
	await signIn(page);
	await page.getByRole('button', { name: 'Use a recovery code instead' }).click();
	await page.getByLabel('Recovery code').fill(owner.recovery);
	await page.getByRole('button', { name: 'Verify' }).click();
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');
});

test('maps problem field errors to their inputs', async ({ page }) => {
	await page.goto('/login');
	await page.getByLabel('Username').fill('bad name');
	await page.getByLabel('Password').fill('x');
	await page.getByRole('button', { name: 'Sign in' }).click();

	const username = page.getByLabel('Username');
	await expect(username).toHaveAttribute('aria-invalid', 'true');
	await expect(username).toHaveAccessibleDescription('must not contain spaces');
	await expect(page.getByRole('alert')).toContainText('invalid sign-in request');
	await expect(page.getByRole('alert')).toContainText('req-e2e');
});

test('signs out with the CSRF token', async ({ page, api }) => {
	api.signedIn = true;
	await page.goto('/data');
	await page.getByRole('button', { name: 'Sign out' }).click();
	await expect(page).toHaveURL('/login');
	expect(api.logoutTokens).toEqual(['synthetic-csrf']);

	await page.goto('/data');
	await expect(page).toHaveURL('/login?next=%2Fdata');
});

test('an expired session redirects to login and back', async ({ page, api }) => {
	api.signedIn = true;
	api.versionStatus = 401; // the session ends right after the guard's check
	await page.goto('/connections?tab=history');

	await expect(page).toHaveURL('/login?next=%2Fconnections%3Ftab%3Dhistory&reason=expired');
	await expect(page.getByRole('status')).toContainText('Your session has expired');

	api.signedIn = false;
	api.versionStatus = 200;
	await signIn(page);
	await expect(page).toHaveURL('/connections?tab=history');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Connections');
});

test('ignores off-site return paths', async ({ page }) => {
	await page.goto('/login?next=//evil.example/');
	await signIn(page);
	await expect(page).toHaveURL('/');
});
