import { expect, test } from './settings-fake';

test('add a timezone period and see the recompute notice, then remove it', async ({ page, settings }) => {
	await page.goto('/settings');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Settings');
	await expect(page.getByRole('row', { name: /Europe\/London/ })).toBeVisible();

	await page.getByRole('combobox', { name: 'Timezone' }).fill('Mars/Olympus');
	await page.getByLabel('Starts at').fill('2026-03-01T00:00');
	await page.getByRole('button', { name: 'Add period' }).click();
	await expect(page.getByText('Enter an IANA timezone such as Europe/Berlin.')).toBeVisible();

	// The start is the wall-clock time in the new zone: Berlin is UTC+1 in March.
	await page.getByRole('combobox', { name: 'Timezone' }).fill('Europe/Berlin');
	await page.getByRole('button', { name: 'Add period' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'being recomputed' })).toContainText('Local dates from 2026-03-01 00:00 (Europe/Berlin) onward');
	expect(settings.periodBodies.at(-1)).toEqual({ tz: 'Europe/Berlin', valid_from: '2026-02-28T23:00:00.000Z' });
	const berlin = page.getByRole('row', { name: /Europe\/Berlin/ });
	await expect(berlin.getByText('Current')).toBeVisible();

	await berlin.getByRole('button', { name: 'Remove Europe/Berlin period' }).click();
	await berlin.getByRole('button', { name: 'Confirm remove' }).click();
	await expect(page.getByRole('row', { name: /Europe\/Berlin/ })).toHaveCount(0);
	await expect(page.getByText('local dates are being recomputed')).toBeVisible();
});

test('create an API key, see the secret once, revoke it', async ({ page, settings }) => {
	await page.goto('/settings/api-keys');
	await expect(page.getByText('No API keys yet.')).toBeVisible();

	await page.getByLabel('Name').fill('home script');
	await page.getByLabel('read:config').check();
	await page.getByRole('button', { name: 'Create key' }).click();
	expect(settings.keyBodies).toEqual([{ name: 'home script', scopes: ['read:health', 'read:config'] }]);

	await expect(page.getByLabel('API key secret')).toHaveText('vmx_synthetic_secret_once');
	await expect(page.getByText('It is shown once')).toBeVisible();
	await page.getByRole('button', { name: 'I have saved it' }).click();
	await expect(page.getByText('vmx_synthetic_secret_once')).toHaveCount(0);
	await page.reload();
	await expect(page.getByText('vmx_synthetic_secret_once')).toHaveCount(0);

	const row = page.getByRole('row', { name: /home script/ });
	await expect(row.getByText('Active')).toBeVisible();
	await row.getByRole('button', { name: 'Revoke key home script' }).click();
	await row.getByRole('button', { name: 'Confirm revoke' }).click();
	await expect(row.getByText('Revoked')).toBeVisible();
	await expect(row.getByRole('button', { name: /Revoke/ })).toHaveCount(0);
});

test('export: start, wait, then the one-time download link appears', async ({ page, settings }) => {
	await page.goto('/settings/backups');
	await expect(page.getByText(/Last backup: .*2026/)).toBeVisible();

	await page.getByLabel('NDJSON plus').check();
	await page.getByRole('button', { name: 'Start export' }).click();
	await expect(page.getByText(/Export (queued|running)/)).toBeVisible();
	expect(settings.exportBody).toEqual({ format: 'csv', include_raw: false });

	const link = page.getByRole('link', { name: 'Download export' });
	await expect(link).toBeVisible({ timeout: 10_000 });
	await expect(link).toHaveAttribute('href', '/api/v1/exports/exp1/download?token=one-time');
	await expect(page.getByText('2.0 MiB')).toBeVisible();
	await expect(page.getByText('The link works once')).toBeVisible();
});

test('AI providers: enabling one saves only that key', async ({ page, settings }) => {
	await page.goto('/settings/ai');
	await expect(page.getByText('its PDF is sent to OpenAI for extraction').first()).toBeVisible();
	await page.locator('input[name="documents.external_ai.openai.enabled"]').check();
	await page.getByRole('button', { name: 'Save' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Saved.' })).toBeVisible();
	expect(settings.patches).toEqual([{ 'documents.external_ai.openai.enabled': true }]);
});

test('source order: connected providers join the saved order; reorder by keyboard and save', async ({ page, settings }) => {
	const connections = [{ provider: 'withings' }, { provider: 'whoop' }, { provider: 'manual' }];
	await page.route('**/api/v1/connections', (r) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ connections }) }));
	await page.goto('/settings/sources');
	const items = page.getByRole('list', { name: 'Source order, first preferred' }).getByRole('listitem');
	await expect(items).toHaveText([/whoop/i, /withings/i]);

	await page.getByRole('button', { name: /Move withings up/i }).click();
	await expect(items).toHaveText([/withings/i, /whoop/i]);
	await expect(page.getByRole('button', { name: /Move withings down/i })).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(items).toHaveText([/whoop/i, /withings/i]);
	await expect(page.getByRole('button', { name: /Move withings up/i })).toBeFocused();
	await page.keyboard.press('Enter');
	await page.getByRole('button', { name: 'Save order' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Saved.' })).toBeVisible();
	expect(settings.patches).toEqual([{ 'sources.priority': ['withings', 'whoop'] }]);
});

test('retention: typed controls, a generic extra key, and a field error', async ({ page, settings }) => {
	await page.goto('/settings/retention');
	await expect(page.getByLabel('Days to keep raw payloads for withings')).toHaveValue('90');
	await expect(page.getByLabel('retention.future_flag')).not.toBeChecked();
	await expect(page.getByText('Pruned raw payloads can no longer be reprocessed')).toBeVisible();

	await page.getByLabel('Delete originals after (days)').fill('365');
	await page.getByLabel('retention.future_flag').check();
	await page.getByLabel('Keep ingest replay records (days)').fill('3');
	await page.getByRole('button', { name: 'Save retention' }).click();
	await expect(page.getByText('Enter a whole number from 7 to 36500.')).toBeVisible();
	expect(settings.patches).toEqual([]);

	await page.getByLabel('Keep ingest replay records (days)').fill('45');
	await page.getByLabel('Days to keep raw payloads for withings').fill('30');
	await page.getByRole('button', { name: 'Save retention' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Saved.' })).toBeVisible();
	expect(settings.patches).toEqual([
		{
			'documents.retention_days': 365,
			'retention.raw_days': { withings: 30 },
			'retention.idempotency_key_days': 45,
			'retention.future_flag': true
		}
	]);
});

test('security: change the password, then sign out one and all other sessions', async ({ page, settings }) => {
	await page.goto('/settings/security');
	await expect(page.getByRole('row')).toHaveCount(4); // header plus three sessions
	await expect(page.getByRole('cell', { name: 'This browser' })).toBeVisible();

	await page.getByLabel('Current password').fill('wrong-synthetic-password');
	await page.getByLabel('New password').fill('synthetic-password-renewed');
	await page.getByRole('button', { name: 'Change password' }).click();
	await expect(page.getByText('does not match')).toBeVisible();
	await page.getByLabel('Current password').fill('synthetic-password');
	await page.getByRole('button', { name: 'Change password' }).click();
	await expect(page.getByText('Every other session was signed out.')).toBeVisible();
	expect(settings.passwordBodies.at(-1)).toEqual({ current_password: 'synthetic-password', new_password: 'synthetic-password-renewed' });
	await expect(page.getByRole('row')).toHaveCount(2);
	await expect(page.getByRole('button', { name: 'Sign out other sessions' })).toBeDisabled();

	settings.sessions.push({ ...settings.sessions[0], id: '00000000-0000-4000-8000-0000000000c4', current: false },
		{ ...settings.sessions[0], id: '00000000-0000-4000-8000-0000000000c5', current: false });
	await page.reload();
	await page.getByRole('button', { name: /^Sign out session from/ }).first().click();
	await expect(page.getByText('Session signed out.')).toBeVisible();
	await expect(page.getByRole('row')).toHaveCount(3);
	await page.getByRole('button', { name: 'Sign out other sessions' }).click();
	await expect(page.getByText('Other sessions signed out.')).toBeVisible();
	await expect(page.getByRole('row')).toHaveCount(2);
	expect(settings.sessions.map((s) => s.current)).toEqual([true]);
});

test('security: set up TOTP, see recovery codes once, turn it off', async ({ page }) => {
	await page.goto('/settings/security');

	await page.getByRole('button', { name: 'Set up two-factor' }).click();
	await expect(page.getByLabel('Setup key')).toHaveText('SYNTHETICSECRET');
	await page.getByLabel('Code from the app').fill('000000');
	await page.getByRole('button', { name: 'Confirm and turn on' }).click();
	await expect(page.getByText('code does not match')).toBeVisible();
	await page.getByLabel('Code from the app').fill('123456');
	await page.getByRole('button', { name: 'Confirm and turn on' }).click();

	await expect(page.getByText('synthetic-recovery-a')).toBeVisible();
	await expect(page.getByText('Two-factor authentication is on.')).toBeVisible();
	await page.getByRole('button', { name: 'I have saved them' }).click();
	await expect(page.getByText('synthetic-recovery-a')).toHaveCount(0);

	await page.getByRole('button', { name: 'Turn off…' }).click();
	await page.getByRole('textbox', { name: 'Password', exact: true }).fill('synthetic-password');
	await page.getByLabel('Authenticator code').fill('123456');
	await page.getByRole('button', { name: 'Turn off two-factor' }).click();
	await expect(page.getByText('Two-factor authentication is off.').first()).toBeVisible();
});

test('system: versions, sizes and degraded connections; a missing status is explained', async ({ page, settings }) => {
	await page.goto('/settings/system');
	await expect(page.getByRole('heading', { name: 'Versions' })).toBeVisible();
	await expect(page.getByText('0.0.0-e2e', { exact: true })).toBeVisible();
	await expect(page.getByText('50.0 MiB')).toBeVisible();
	await expect(page.getByText('1.0 MiB')).toBeVisible();
	await expect(page.getByRole('listitem').filter({ hasText: 'whoop' })).toContainText('schema_drift');
	await expect(page.getByText('No job is failing.')).toBeVisible();

	settings.status = null;
	await page.reload();
	await expect(page.getByText('System status is not available from this server yet.')).toBeVisible();
	await expect(page.getByText('0.0.0-e2e', { exact: true })).toBeVisible();
});
