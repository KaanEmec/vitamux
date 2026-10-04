import { expect, ids, test } from './connections-fake';

test('Today: alerts for a degraded connection, key metrics with sources, health cards', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Today');

	const alerts = page.getByRole('region', { name: 'Alerts' });
	await expect(alerts.getByText('Ultrahuman is degraded: ultrahuman.metrics: response shape changed (schema_drift)')).toBeVisible();
	await expect(alerts.getByText('Withings needs reauthorization.')).toBeVisible();
	await expect(alerts.getByText(/Job normalize_batch failed permanently after 5 attempts/)).toBeVisible();
	await alerts.getByRole('link', { name: 'See streams' }).click();
	await expect(page).toHaveURL(`/connections/${ids.ultrahuman}?tab=streams`);
	await expect(page.getByRole('row', { name: /ultrahuman\.metrics/ }).getByText('Degraded')).toBeVisible();

	await page.goto('/');
	const tile = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'Resting heart rate' }) });
	await expect(tile.getByText('52 bpm')).toBeVisible();
	await expect(tile.getByText('Fallback')).toBeVisible();
	await expect(tile.getByRole('list', { name: 'Sources' })).toHaveText('watch · Apple Health');
	const steps = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'Steps' }) });
	await expect(steps.getByText('Yesterday')).toBeVisible();

	const health = page.getByRole('region', { name: 'Connections' });
	await expect(health.getByRole('listitem').filter({ hasText: 'Apple Health' }).getByText('Healthy')).toBeVisible();
	await expect(health.getByRole('listitem').filter({ hasText: 'Ultrahuman' }).getByText('Unofficial')).toBeVisible();
});

test('Today without resolved values still shows health', async ({ page, conns }) => {
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
	const card = page.getByRole('article', { name: 'Withings' });
	await expect(card.getByText('Healthy')).toBeVisible();
	await card.getByRole('link', { name: 'Withings' }).click();
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

test('cards show health, the 14-day run strip and the fix-it action', async ({ page }) => {
	await page.goto('/connections');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Connections');
	await expect(page.getByText('3 sources · 1 healthy · 2 need attention')).toBeVisible();

	const ultra = page.getByRole('article', { name: 'Ultrahuman' });
	await expect(ultra.getByText('Unofficial API', { exact: true })).toBeVisible();
	await expect(ultra.getByText('Degraded')).toBeVisible();
	const strip = ultra.getByRole('img', { name: /^Sync runs, last 14 days/ });
	await expect(strip).toHaveAccessibleName('Sync runs, last 14 days: 13 days with successful runs, 1 day with a failed run, 0 days without runs');
	await expect(strip.locator('.cell')).toHaveCount(14);
	await expect(strip.locator('.cell.fail')).toHaveCount(1);
	await expect(strip.locator('.cell.ok')).toHaveCount(13);
	await expect(ultra.getByText('14 runs')).toBeVisible();
	await ultra.getByRole('link', { name: 'See streams' }).click();
	await expect(page).toHaveURL(`/connections/${ids.ultrahuman}?tab=streams`);

	await page.goto('/connections');
	const withings = page.getByRole('article', { name: 'Withings' });
	await expect(withings.getByRole('img', { name: 'Sync runs, last 14 days: 0 days with successful runs, 0 days with a failed run, 14 days without runs' })).toBeVisible();
	await expect(withings.getByRole('button', { name: 'Reauthorize' })).toBeVisible();
	await expect(withings.getByRole('button', { name: 'Sync now' })).toHaveCount(0);
	await expect(page.getByRole('article', { name: 'Apple Health' }).getByRole('button')).toHaveCount(0);

	const recent = page.getByRole('region', { name: 'Recent sync runs' });
	await expect(recent.getByRole('row')).toHaveCount(9); // header and the 8 latest of Ultrahuman's 14
	await expect(recent.getByRole('row', { name: /Ultrahuman/ }).first()).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Backfill in progress' })).toBeVisible();
});

test('sync from a card refreshes its run strip', async ({ page, conns }) => {
	conns.connections = conns.connections.filter((c) => c.provider === 'ultrahuman');
	await page.goto('/connections');
	const card = page.getByRole('article', { name: 'Ultrahuman' });
	await expect(card.getByText('14 runs')).toBeVisible();
	await card.getByRole('button', { name: 'Sync now' }).click();
	await expect(card.getByRole('status')).toContainText('Sync queued: ultrahuman.metrics (queued)');
	await expect(card.getByText('15 runs')).toBeVisible();
});

test('detail: header actions and the sync timeline', async ({ page }) => {
	await page.goto(`/connections/${ids.ultrahuman}`);
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Ultrahuman');
	await expect(page.getByRole('button', { name: 'Sync now' })).toBeEnabled();
	await expect(page.getByRole('button', { name: 'Reauthorize' })).toBeVisible();
	const timeline = page.getByRole('region', { name: 'Sync timeline' });
	await expect(timeline.getByRole('img', { name: /1 day with a failed run/ })).toBeVisible();
	await expect(timeline.getByRole('list', { name: 'Latest runs' }).getByRole('listitem')).toHaveCount(5);
	await expect(timeline.getByRole('link', { name: 'All runs' })).toHaveAttribute('href', '?tab=history');
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
	await expect(page.getByRole('article', { name: 'Withings' }).getByText('Healthy')).toBeVisible();
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
	await expect(page.getByRole('article', { name: 'Ultrahuman' })).toHaveCount(0);
	expect(conns.deletes).toEqual(['data=delete']);
});
