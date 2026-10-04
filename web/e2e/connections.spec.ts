import { expect, ids, test } from './connections-fake';

test('Dashboard: alerts for a degraded connection, health cards', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Dashboard');

	const alerts = page.getByRole('region', { name: 'Alerts' });
	await expect(alerts.getByText('Ultrahuman is degraded: ultrahuman.metrics: response shape changed (schema_drift)')).toBeVisible();
	await expect(alerts.getByText('Withings needs reauthorization.')).toBeVisible();
	await expect(alerts.getByText(/Job normalize_batch failed permanently after 5 attempts/)).toBeVisible();
	await alerts.getByRole('link', { name: 'See streams' }).click();
	await expect(page).toHaveURL(`/connections/${ids.ultrahuman}?tab=streams`);
	await expect(page.getByRole('row', { name: /ultrahuman\.metrics/ }).getByText('Degraded')).toBeVisible();

	await page.goto('/');
	const health = page.getByRole('region', { name: 'Connections' });
	await expect(health.getByRole('listitem').filter({ hasText: 'Apple Health' }).getByText('Healthy')).toBeVisible();
	await expect(health.getByRole('listitem').filter({ hasText: 'Ultrahuman' }).getByText('Unofficial')).toBeVisible();
});

test('Dashboard without resolved values still shows health', async ({ page, conns }) => {
	conns.resolvedStatus = 503;
	await page.goto('/');
	await expect(page.getByText('Resolved values are not available yet.')).toBeVisible();
	await expect(page.getByRole('region', { name: 'Alerts' }).getByText('Withings needs reauthorization.')).toBeVisible();
});

test('connect the OAuth provider, sync, start and cancel a backfill', async ({ page, conns }) => {
	conns.connections = conns.connections.filter((c) => c.provider !== 'withings');
	await page.goto('/connections');
	await page.getByRole('button', { name: 'Connect a source' }).click();
	const wizard = page.getByRole('dialog', { name: 'Connect a source' });
	await expect(wizard.getByRole('radio', { name: 'Withings' })).toBeChecked();
	await wizard.getByRole('button', { name: 'Continue to Withings' }).click();

	await expect(page).toHaveURL('/connections?connected=withings');
	await expect(page.getByRole('status').filter({ hasText: 'Withings is connected' })).toBeVisible();
	const row = page.getByRole('row', { name: /Withings/ });
	await expect(row.getByText('Healthy')).toBeVisible();
	await row.getByRole('link', { name: 'Withings' }).click();
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Withings');

	await page.getByRole('button', { name: 'Sync now' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Sync queued: withings.measures (queued)' })).toBeVisible();
	const tabs = page.getByRole('navigation', { name: 'Connection sections' });
	await tabs.getByRole('link', { name: 'History' }).click();
	await expect(page.getByRole('row', { name: /sync/ }).getByText('succeeded')).toBeVisible();

	await tabs.getByRole('link', { name: 'Backfills' }).click();
	await page.getByRole('button', { name: 'New backfill' }).click();
	const dialog = page.getByRole('dialog', { name: 'Backfill Withings' });
	await expect(dialog.getByLabel('Stream')).toHaveValue('withings.measures');
	await expect(dialog.getByText(/units of 30 days \(about 13 units\)/)).toBeVisible();
	await dialog.getByRole('button', { name: 'Start backfill' }).click();
	await expect(dialog).toBeHidden();
	await expect(page.getByRole('status').filter({ hasText: 'Backfill of withings.measures started.' })).toBeVisible();
	const backfill = page.getByRole('row', { name: /withings\.measures/ });
	await expect(backfill.getByText('running')).toBeVisible();
	await expect(backfill.getByText('0/13 done')).toBeVisible();

	await backfill.getByRole('button', { name: /^Cancel/ }).click();
	await expect(backfill.getByText('cancelled')).toBeVisible();
	await expect(page.getByRole('status').filter({ hasText: 'cancelled; data fetched so far stays' })).toBeVisible();
	await expect(backfill.getByRole('button', { name: /^Cancel/ })).toHaveCount(0);
});

test('retry failed backfill units', async ({ page }) => {
	await page.goto(`/connections/${ids.ultrahuman}?tab=backfills`);
	const backfill = page.getByRole('row', { name: /ultrahuman\.metrics/ }).first();
	await expect(backfill.getByText('1 failed')).toBeVisible();
	await backfill.getByRole('button', { name: /^Units of/ }).click();
	await expect(page.getByRole('table', { name: /Units of/ }).getByText('upstream_5xx')).toBeVisible();
	await backfill.getByRole('button', { name: /^Retry failed/ }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Failed units of ultrahuman.metrics queued again.' })).toBeVisible();
	await expect(backfill.getByText('running')).toBeVisible();
	await expect(backfill.getByText('1 failed')).toBeHidden();
});

test('complete reauthorization of a connection', async ({ page }) => {
	await page.goto(`/connections/${ids.withings}`);
	await expect(page.getByRole('alert')).toContainText('no longer accepts the stored authorization');
	await page.getByRole('button', { name: 'Reauthorize' }).click();

	await expect(page).toHaveURL('/connections?connected=withings');
	await expect(page.getByRole('row', { name: /Withings/ }).getByText('Healthy')).toBeVisible();
	await page.goto('/');
	await expect(page.getByRole('region', { name: 'Alerts' }).getByText('Ultrahuman is degraded', { exact: false })).toBeVisible();
	await expect(page.getByText('Withings needs reauthorization.')).toHaveCount(0);
});

test('pause, resume, and delete a connection with its data', async ({ page, conns }) => {
	await page.goto(`/connections/${ids.ultrahuman}?tab=settings`);
	await page.getByRole('button', { name: 'Pause syncing' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Syncing is paused.' })).toBeVisible();
	await page.getByRole('button', { name: 'Resume syncing' }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Syncing resumed.' })).toBeVisible();

	await page.getByRole('button', { name: 'Remove connection…' }).click();
	const dialog = page.getByRole('dialog', { name: 'Remove the Ultrahuman connection' });
	await dialog.getByRole('radio', { name: /Delete the connection and its data/ }).check();
	const confirm = dialog.getByRole('button', { name: 'Delete connection and data' });
	await expect(confirm).toBeDisabled();
	await dialog.getByRole('checkbox').check();
	await confirm.click();

	await expect(page).toHaveURL('/connections?removed=ultrahuman');
	await expect(page.getByRole('status').filter({ hasText: 'The Ultrahuman connection was removed.' })).toBeVisible();
	await expect(page.getByRole('row', { name: /Ultrahuman/ })).toHaveCount(0);
	expect(conns.deletes).toEqual(['data=delete']);
});
