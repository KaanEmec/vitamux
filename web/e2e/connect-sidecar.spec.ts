// The generic auth wizard (J17.2): provider list from GET /providers, a prompt step, an MFA
// prompt step, the redirect step, a rejected step and reauthorization of a sidecar connection.
// Values are synthetic (sidecarSecret in connections-fake.ts).
import { conn, expect, sidecarSecret, test } from './connections-fake';

test('providers: unofficial badge and paused note, unavailable sidecar disabled with a reason', async ({ page }) => {
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const wizard = page.getByRole('dialog', { name: 'Connect a source' });
	await expect(wizard.getByRole('radio', { name: 'Withings' })).toBeChecked();

	const sidecar = wizard.getByRole('radio', { name: /Example Collector/ });
	await expect(sidecar).toBeEnabled();
	await expect(sidecar).toHaveAccessibleName('Example Collector Unofficial API');
	await expect(sidecar).toHaveAccessibleDescription(/unofficial API that can change without notice.*starts paused/);

	const offline = wizard.getByRole('radio', { name: /Offline sidecar/ });
	await expect(offline).toBeDisabled();
	await expect(offline).toHaveAccessibleDescription(/Not available/);
});

test('connect through a prompt and an MFA prompt, then open the paused connection', async ({ page, conns }) => {
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const wizard = page.getByRole('dialog', { name: 'Connect a source' });
	await wizard.getByRole('radio', { name: /Example Collector/ }).check();
	await wizard.getByRole('button', { name: 'Continue to Example Collector' }).click();

	await expect(wizard.getByText('Sign in to Example Collector.')).toBeVisible();
	await expect(wizard.getByLabel('Username')).toHaveAttribute('type', 'text');
	await expect(wizard.getByLabel('Password')).toHaveAttribute('type', 'password');
	await wizard.getByLabel('Username').fill(sidecarSecret.username);
	await wizard.getByLabel('Password').fill(sidecarSecret.password);
	await wizard.getByRole('button', { name: 'Continue' }).click();

	const code = wizard.getByLabel('Verification code');
	await expect(code).toBeFocused();
	await expect(code).toHaveAttribute('inputmode', 'numeric');
	await expect(code).toHaveAttribute('autocomplete', 'one-time-code');
	await expect(wizard.getByLabel('Password')).toHaveCount(0);
	await code.fill(sidecarSecret.code);
	await wizard.getByRole('button', { name: 'Continue' }).click();

	await expect(page).toHaveURL(/\/connections\/conn_[0-9a-f]{32}$/);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Example Collector');
	await expect(page).toHaveTitle('Example Collector · Vitamux');
	await expect(page.getByText('Unofficial API', { exact: true })).toBeVisible();
	await expect(page.getByText('Paused: resume it in Settings to sync.')).toBeVisible();
	await expect(page.getByRole('link', { name: 'example-collector' })).toHaveAttribute('href', 'https://example.com/example-collector');
	await expect(page.getByText('1.4.2')).toBeVisible();
	await page.goto('/connections');
	await expect(page.getByRole('link', { name: 'Example Collector' })).toBeVisible();
	// A provider the list does not know falls back to its code.
	await expect(page.getByRole('link', { name: 'Ultrahuman' })).toBeVisible();
	expect(conns.continues.map((c) => Object.keys(c.values))).toEqual([['username', 'password'], ['code']]);
});

test('a redirect step leaves for the provider', async ({ page, conns }) => {
	conns.connections = conns.connections.filter((c) => c.provider !== 'withings');
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	await page.getByRole('dialog').getByRole('button', { name: 'Continue to Withings' }).click();
	await expect(page).toHaveURL('/connections?connected=withings');
});

test('a rejected step shows the problem and starts again from the provider list', async ({ page, conns }) => {
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const wizard = page.getByRole('dialog', { name: 'Connect a source' });
	await wizard.getByRole('radio', { name: /Example Collector/ }).check();
	await wizard.getByRole('button', { name: 'Continue to Example Collector' }).click();
	await wizard.getByLabel('Username').fill(sidecarSecret.username);
	await wizard.getByLabel('Password').fill('synthetic-wrong');
	await wizard.getByRole('button', { name: 'Continue' }).click();

	await expect(wizard.getByRole('alert')).toContainText('the connector did not accept the sign-in details');
	await expect(wizard.getByLabel('Password')).toHaveCount(0);
	await wizard.getByRole('button', { name: 'Start again' }).click();
	await expect(wizard.getByRole('radio', { name: 'Withings' })).toBeVisible();
	expect(conns.continues).toHaveLength(1);
});

test('reauthorize a sidecar connection through its prompts', async ({ page, conns }) => {
	const c = conn('conn_' + 'f'.repeat(32), 'example_sidecar', {
		mode: 'remote', official: false, status: 'needs_reauth', health: 'needs_reauth', health_reason: 'sign-in expired',
		upstream: { package: 'example-collector', version: '1.4.2', source_url: 'https://example.com/example-collector' }
	});
	conns.connections.push(c);
	await page.goto(`/connections/${c.id}`);
	await page.getByRole('button', { name: 'Reauthorize' }).click();
	const dialog = page.getByRole('dialog', { name: 'Reauthorize Example Collector' });
	await dialog.getByLabel('Username').fill(sidecarSecret.username);
	await dialog.getByLabel('Password').fill(sidecarSecret.password);
	await dialog.getByRole('button', { name: 'Continue' }).click();
	await dialog.getByLabel('Verification code').fill(sidecarSecret.code);
	await dialog.getByRole('button', { name: 'Continue' }).click();

	await expect(page).toHaveURL(`/connections/${c.id}`);
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('region', { name: 'Overview' }).getByText('Healthy')).toBeVisible();
	await expect(page.getByText('no longer accepts the stored authorization')).toHaveCount(0);
});
