// Real-stack smoke: the embedded SPA and the Go API on a freshly migrated database whose only
// row is the owner (scripts/e2e-stack.sh creates it and passes the sign-in in the environment).
// Signs in through the form, opens Dashboard, Explore (with a metric page and its rule lens),
// Rules, Connections and Settings (with Sources), and expects every /api/v1 answer
// after sign-in to be a 2xx (apart from the endpoints listed below). Run with `--project=stack`; skipped without the stack variables.
import { expect, test } from '@playwright/test';

// Documented in api/openapi.yaml but not served yet: a 404 is tolerated for these until they
// land. Delete the entry then.
const notServedYet = new Set<string>([]);
const username = process.env.VITAMUX_E2E_USER;
const password = process.env.VITAMUX_E2E_PASSWORD;
test.skip(!username || !password, 'run via scripts/e2e-stack.sh');

test('sign in, then Dashboard, Explore, Rules, Connections and Settings load from the real API', async ({ page }) => {
	const failures: string[] = [];
	const errors: string[] = [];
	page.on('pageerror', (e) => errors.push(e.message));
	let signedIn = false;
	page.on('response', (r) => {
		const path = new URL(r.url()).pathname;
		if (signedIn && path.startsWith('/api/v1/') && !r.ok() && !(r.status() === 404 && notServedYet.has(path))) failures.push(`${r.status()} ${r.request().method()} ${path}`);
	});

	await page.goto('/');
	await expect(page).toHaveURL(/\/login/);
	await page.getByLabel('Username').fill(username!);
	await page.getByLabel('Password').fill(password!);
	await page.getByRole('button', { name: 'Sign in' }).click();
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');
	signedIn = true;

	for (const [path, heading] of [
		['/', 'Dashboard'],
		['/explore', 'Explore'],
		['/explore/steps', 'Steps'],
		['/rules', 'Rules'],
		['/connections', 'Connections'],
		['/settings', 'Settings'],
		['/settings/sources', 'Settings']
	]) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
		await page.waitForLoadState('networkidle');
	}
	await page.goto('/explore/steps');
	await page.getByRole('button', { name: 'How it’s calculated' }).click();
	await expect(page.getByRole('complementary', { name: 'How this is calculated' })).toBeVisible();
	await page.waitForLoadState('networkidle');
	expect(failures, 'non-2xx API responses after sign-in').toEqual([]);
	expect(errors, 'page errors').toEqual([]);
});
