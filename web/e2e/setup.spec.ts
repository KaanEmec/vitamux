// Guided source setup (E20): the first run, the Withings app wizard, a sidecar that is turned on,
// the Garmin sign-in with MFA, plain-language errors and Settings › Sources. Values are synthetic
// (setup-fake.ts); no secret may reach the URL or browser storage.
import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';
import { expect, garminLogin, sidecarSecret, test, withingsApp } from './setup-fake';

async function noSecretsKept(page: Page, ...secrets: string[]) {
	const kept = await page.evaluate(() => location.href + JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
	for (const s of secrets) expect(kept).not.toContain(s);
}

const card = (page: Page, name: string) => page.locator('.choice').filter({ has: page.getByRole('radio', { name }) });

test('first run: Connections is the setup, with a state and one next action per source', async ({ page }) => {
	await page.goto('/connections');
	const setup = page.getByRole('region', { name: 'Connect a source' });
	await expect(page.getByRole('button', { name: 'Connect a source' })).toHaveCount(0);
	await expect(setup.getByRole('radio', { name: 'Withings' })).toBeChecked();
	await expect(setup.getByRole('radio', { name: 'Withings' })).toHaveAccessibleDescription(/Needs its app credentials/);
	await expect(setup.getByRole('button', { name: 'Set up Withings' })).toBeVisible();

	const garmin = setup.getByRole('radio', { name: /Garmin Connect/ });
	await expect(garmin).toBeDisabled();
	await expect(garmin).toHaveAccessibleDescription(/Not available: its sidecar is not running/);
	await expect(card(page, 'Garmin Connect').getByText('COMPOSE_PROFILES=garmin')).toBeVisible();
	await expect(card(page, 'Garmin Connect').getByText('GARMIN_SIDECAR=1')).toBeVisible();

	await setup.getByRole('radio', { name: /WHOOP/ }).check();
	await expect(setup.getByRole('radio', { name: /WHOOP/ })).toHaveAccessibleName('WHOOP Unofficial API');
	await expect(card(page, 'WHOOP').getByText('@dofek/whoop 0.1.65')).toBeVisible();
	await expect(setup.getByRole('button', { name: 'Continue to WHOOP' })).toBeEnabled();
});

test('Withings: from no app to the OAuth start, a wrong secret reported at verify', async ({ page, context, setup }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Set up Withings' }).click();

	await expect(page.getByRole('heading', { name: 'Create the app' })).toBeVisible();
	await expect(page.getByRole('link', { name: 'Withings developer dashboard' })).toHaveAttribute('href', 'https://developer.withings.com/dashboard/');
	await expect(page.getByText('https://vitamux.example.test/oauth/withings/callback')).toBeVisible();
	await page.getByRole('button', { name: 'Copy Callback URL' }).click();
	await expect(page.getByRole('button', { name: 'Copied Callback URL' })).toBeVisible();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('https://vitamux.example.test/oauth/withings/callback');
	await page.getByRole('button', { name: 'Next' }).click();

	await expect(page.getByRole('heading', { name: 'Paste the credentials' })).toBeVisible();
	await expect(page.getByLabel('Client secret')).toHaveAttribute('type', 'password');
	await page.getByLabel('Client id').fill(withingsApp.id);
	await page.getByLabel('Client secret').fill(withingsApp.wrong);
	await page.getByRole('button', { name: 'Save and check' }).click();
	await expect(page.getByRole('alert')).toContainText('refused the client id and secret');
	await expect(page.getByLabel('Client secret')).toHaveValue('');

	await page.getByLabel('Client secret').fill(withingsApp.secret);
	await page.getByRole('button', { name: 'Save and check' }).click();
	await expect(page.getByRole('heading', { name: 'Connect your account' })).toBeVisible();
	await expect(page.getByText('The provider accepted the client id and secret.')).toBeVisible();
	await page.getByRole('button', { name: 'Continue to Withings' }).click();

	await expect(page).toHaveURL('/connections?connected=withings');
	await expect(page.getByRole('status').filter({ hasText: 'Withings is connected' })).toBeVisible();
	await expect(page.getByRole('article', { name: 'Withings' })).toBeVisible();
	expect(setup.begins).toEqual(['withings']);
	expect(setup.sent.filter((s) => s.route.startsWith('PUT')).map((s) => s.body)).toEqual([
		{ client_id: withingsApp.id, client_secret: withingsApp.wrong },
		{ client_id: withingsApp.id, client_secret: withingsApp.secret }
	]);
	await expect(page.getByText(withingsApp.secret)).toHaveCount(0);
	await noSecretsKept(page, withingsApp.secret);
});

test('Withings set by the environment: read-only, connect goes straight to OAuth', async ({ page, setup }) => {
	setup.withings.app_credentials = { set: true, managed_by_environment: true, client_id: 'env-client', updated_at: null };
	setup.withings.setup_state = 'ready';
	await page.goto('/connections');
	await expect(page.getByText('It uses the app credentials set by the environment.')).toBeVisible();
	await page.getByRole('button', { name: 'Continue to Withings' }).click();
	await expect(page).toHaveURL('/connections?connected=withings');

	await page.goto('/settings/sources');
	const row = page.getByRole('row', { name: /Withings/ });
	await expect(row.getByText('Managed by the environment')).toBeVisible();
	await expect(row.getByText('env-client')).toBeVisible();
	await expect(row.getByText('Read-only')).toBeVisible();
	await expect(row.getByRole('button')).toHaveCount(0);
});

test('an http public address blocks Withings and says what to change', async ({ page, setup }) => {
	setup.withings.setup_state = 'needs_public_url';
	setup.withings.problems = [{ code: 'public_url_not_https', message: 'Set VITAMUX_PUBLIC_URL to the https address of your reverse proxy: providers accept only https callbacks.' }];
	await page.goto('/connections');
	const withings = page.getByRole('radio', { name: 'Withings' });
	await expect(withings).toBeDisabled();
	await expect(withings).toHaveAccessibleDescription(/Needs an https public address.*Set VITAMUX_PUBLIC_URL to the https address/);
	await expect(page.getByRole('radio', { name: /WHOOP/ })).toBeChecked();
});

test('a failed OAuth return points back to the app setup', async ({ page, setup }) => {
	setup.withings.app_credentials = { set: true, managed_by_environment: false, client_id: withingsApp.id, updated_at: '2026-10-01T08:00:00Z' };
	setup.withings.setup_state = 'ready';
	await page.goto('/connections?auth_error=exchange_failed&provider=withings');
	const alert = page.getByRole('alert');
	await expect(alert).toContainText('Connecting Withings failed: The provider did not accept the authorization.');
	await expect(alert).toContainText('Check the client id, the secret and the callback URL of your Withings app.');
	await alert.getByRole('button', { name: 'Review the app setup' }).click();
	const dialog = page.getByRole('dialog', { name: 'Connect a source' });
	await expect(dialog.getByRole('heading', { name: 'Create the app' })).toBeVisible();
	await expect(dialog.getByText('https://vitamux.example.test/oauth/withings/callback')).toBeVisible();
});

test('Garmin sidecar: the enable line, Check again until it answers, then ready', async ({ page, setup }) => {
	await page.goto('/connections');
	const garmin = card(page, 'Garmin Connect');
	await expect(garmin.getByText('The garmin sidecar is not running.')).toBeVisible();
	await garmin.getByRole('button', { name: 'Check again' }).click();
	await expect(garmin.getByRole('status').filter({ hasText: 'still does not answer' })).toBeVisible();
	await garmin.getByRole('button', { name: 'Check again' }).click();

	const radio = page.getByRole('radio', { name: /Garmin Connect/ });
	await expect(radio).toBeEnabled();
	await expect(radio).toHaveAccessibleName('Garmin Connect Unofficial API');
	await expect(garmin.getByText('garminconnect 0.3.17')).toBeVisible();
	await expect(garmin.getByText('COMPOSE_PROFILES=garmin')).toHaveCount(0);
	expect(setup.sent.filter((s) => s.route === 'POST /providers/garmin/probe')).toHaveLength(2);
});

test('Garmin: sign in with email, password and an MFA code in one dialog', async ({ page, setup }) => {
	setup.probesUntilUp = 0;
	setup.connections.push({ id: 'conn_' + 'a'.repeat(32), provider: 'whoop', mode: 'remote', status: 'paused', official: false, upstream: null, health: 'paused', health_reason: null, last_success_at: null, last_error_class: null, consecutive_failures: 0, created_at: '2026-10-01T08:00:00Z', updated_at: '2026-10-01T08:00:00Z' });
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const dialog = page.getByRole('dialog', { name: 'Connect a source' });
	await card(page, 'Garmin Connect').getByRole('button', { name: 'Check again' }).click();
	await dialog.getByRole('radio', { name: /Garmin Connect/ }).check();
	await expect(dialog.getByText(/email and password here, then a verification code/)).toBeVisible();
	await dialog.getByRole('button', { name: 'Continue to Garmin Connect' }).click();

	await expect(dialog.getByLabel('Email')).toHaveAttribute('autocomplete', 'username');
	await expect(dialog.getByLabel('Password')).toHaveAttribute('autocomplete', 'current-password');
	await dialog.getByLabel('Email').fill(garminLogin.email);
	await dialog.getByLabel('Password').fill(garminLogin.password);
	await dialog.getByRole('button', { name: 'Continue' }).click();

	const code = dialog.getByLabel('Verification code');
	await expect(code).toBeFocused();
	await expect(code).toHaveAttribute('autocomplete', 'one-time-code');
	await code.fill(garminLogin.code);
	await dialog.getByRole('button', { name: 'Continue' }).click();

	await expect(page).toHaveURL(/\/connections\/conn_0+\d+$/);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Garmin Connect');
	await noSecretsKept(page, garminLogin.password, garminLogin.code);
});

test('sign-in errors in plain language: password, code, rate limit, sidecar gone', async ({ page, setup }) => {
	setup.probesUntilUp = 0;
	await page.goto('/connections');
	const setupCard = page.getByRole('region', { name: 'Connect a source' });
	const start = async () => {
		await setupCard.getByRole('radio', { name: /WHOOP/ }).check();
		await setupCard.getByRole('button', { name: 'Continue to WHOOP' }).click();
	};
	const signIn = async (password = garminLogin.password) => {
		await setupCard.getByLabel('Email').fill(garminLogin.email);
		await setupCard.getByLabel('Password').fill(password);
		await setupCard.getByRole('button', { name: 'Continue' }).click();
	};

	await start();
	await signIn('synthetic-wrong');
	await expect(setupCard.getByRole('alert')).toContainText('WHOOP did not accept the email or password.');
	await setupCard.getByRole('button', { name: 'Start again' }).click();

	await start();
	await signIn();
	await setupCard.getByLabel('Verification code').fill('000000');
	await setupCard.getByRole('button', { name: 'Continue' }).click();
	await expect(setupCard.getByRole('alert')).toContainText('WHOOP did not accept the verification code, or it expired. Start again and use the newest code.');
	await setupCard.getByRole('button', { name: 'Start again' }).click();

	await start();
	setup.failNext = 429;
	await signIn();
	await expect(setupCard.getByRole('alert')).toContainText('WHOOP is limiting sign-in attempts. Wait 2 minutes, then start again.');
	await setupCard.getByRole('button', { name: 'Start again' }).click();

	await start();
	setup.failNext = 503;
	await signIn();
	await expect(setupCard.getByRole('alert')).toContainText('The WHOOP sidecar is not running');
	await setupCard.getByRole('button', { name: 'Start again' }).click();
	await expect(setupCard.getByRole('radio', { name: /WHOOP/ })).toBeDisabled();
	await noSecretsKept(page, garminLogin.password);
});

test('Settings › Sources: replace and remove app credentials, add and remove a sidecar', async ({ page, context, setup }) => {
	await context.grantPermissions(['clipboard-read', 'clipboard-write']);
	setup.withings.app_credentials = { set: true, managed_by_environment: false, client_id: 'old-client', updated_at: '2026-10-01T08:00:00Z' };
	setup.withings.setup_state = 'ready';
	setup.appInUse = true;
	await page.goto('/settings/sources');
	await expect(page.getByRole('heading', { level: 2, name: 'Sources' })).toBeVisible();

	const app = page.getByRole('region', { name: 'App credentials' });
	await app.getByRole('button', { name: 'Replace the Withings app credentials' }).click();
	await expect(app.getByLabel('Client id')).toHaveValue('old-client');
	await app.getByLabel('Client secret').fill(withingsApp.secret);
	await app.getByRole('button', { name: 'Save and check' }).click();
	await expect(app.getByRole('status')).toContainText('Saved the Withings app credentials. The provider accepted the client id and secret.');
	await expect(app.getByRole('row', { name: /Withings/ }).getByText('Set', { exact: true })).toBeVisible();

	await app.getByRole('button', { name: 'Remove the Withings app credentials' }).click();
	await app.getByRole('button', { name: 'Confirm remove' }).click();
	await expect(app.getByRole('alert')).toContainText('Connections still use them.');
	await app.getByRole('button', { name: 'Remove anyway' }).click();
	await expect(app.getByRole('status')).toContainText('Removed the Withings app credentials.');
	await expect(app.getByRole('row', { name: /Withings/ }).getByText('Not set')).toBeVisible();
	expect(setup.sent.filter((s) => s.route.startsWith('DELETE')).map((s) => s.route)).toEqual([
		'DELETE /providers/withings/app-credentials',
		'DELETE /providers/withings/app-credentials?confirm=true'
	]);

	const sidecars = page.getByRole('region', { name: 'Sidecars' });
	await expect(sidecars.getByRole('row', { name: /garmin/ }).getByText('Read-only')).toBeVisible();
	const add = page.getByRole('region', { name: 'Add a sidecar' });
	await add.getByLabel('Name').fill('ultrahuman');
	await add.getByLabel('Private address').fill('https://public.example.test');
	await add.getByRole('button', { name: 'Add sidecar' }).click();
	await expect(add.getByText('must resolve to a loopback, private or link-local address')).toBeVisible();
	await add.getByLabel('Private address').fill('http://ultrahuman:8080');
	await add.getByRole('button', { name: 'Add sidecar' }).click();
	await expect(add.getByText(sidecarSecret)).toBeVisible();
	await add.getByRole('button', { name: 'Copy Shared secret' }).click();
	expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(sidecarSecret);
	await add.getByRole('button', { name: 'I have saved it' }).click();
	await expect(page.getByText(sidecarSecret)).toHaveCount(0);
	await noSecretsKept(page, sidecarSecret);

	const row = sidecars.getByRole('row', { name: /ultrahuman/ });
	await expect(row.getByText('Not answering')).toBeVisible();
	await row.getByRole('button', { name: 'Remove the sidecar ultrahuman' }).click();
	await row.getByRole('button', { name: 'Confirm remove' }).click();
	await expect(sidecars.getByRole('status')).toContainText('Removed the sidecar “ultrahuman”.');
	await expect(sidecars.getByRole('row', { name: /ultrahuman/ })).toHaveCount(0);
});

test('no serious axe violations in setup', async ({ page, setup }) => {
	const scan = async (what: string) => {
		await page.waitForLoadState('networkidle');
		for (const scheme of ['light', 'dark'] as const) {
			await page.emulateMedia({ colorScheme: scheme });
			const { violations } = await new AxeBuilder({ page }).analyze();
			const blocking = violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
			expect(blocking.map((v) => `${what} [${scheme}]: ${v.id} ${v.nodes.map((n) => n.target.join(' ')).join(' | ')}`)).toEqual([]);
		}
	};
	setup.probesUntilUp = 0;
	await page.goto('/connections');
	await expect(page.getByRole('button', { name: 'Set up Withings' })).toBeVisible();
	await scan('first run');
	await page.getByRole('button', { name: 'Set up Withings' }).click();
	await scan('app wizard, step 1');
	await page.getByRole('button', { name: 'Next' }).click();
	await scan('app wizard, step 2');
	await page.getByRole('button', { name: 'Back', exact: true }).click();
	await page.getByRole('button', { name: 'Back', exact: true }).click();
	await page.getByRole('radio', { name: /WHOOP/ }).check();
	await page.getByRole('button', { name: 'Continue to WHOOP' }).click();
	await expect(page.getByLabel('Password')).toBeVisible();
	await scan('sign-in');
	await page.goto('/settings/sources');
	await expect(page.getByRole('region', { name: 'Sidecars' }).getByRole('row', { name: /whoop/ })).toBeVisible();
	await scan('settings, sources');
	await page.setViewportSize({ width: 375, height: 800 });
	await page.goto('/connections');
	await expect(page.getByRole('button', { name: 'Set up Withings' })).toBeVisible();
	await scan('first run, phone');
	const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
	expect(overflow).toBeLessThanOrEqual(0);
});
