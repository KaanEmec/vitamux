// The small charts of the kit on dashboard cards (J23.4): Sparkline and StageStack, in both
// themes. They have no points to visit, so their values are in words beside them (the card's
// value and the stage legend) rather than behind a table. Synthetic data (dashboard-fake.ts).
import { schemes } from './charts';
import { expect, test } from './dashboard-fake';

for (const scheme of schemes) {
	test(`Sparkline and StageStack (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/');
		const sleep = page.getByRole('article', { name: 'Sleep', exact: true });
		const stack = sleep.getByRole('img', { name: 'Time in each sleep stage' });
		await expect(stack).toBeVisible();
		await expect(stack.locator('.seg.deep')).toHaveCount(1);
		await expect(sleep.getByRole('listitem').filter({ hasText: 'REM' })).toContainText(/\d+h \d{2}m/);

		const rhr = page.getByRole('article', { name: 'Resting heart rate', exact: true });
		const spark = rhr.locator('svg.spark');
		await expect(spark).toBeVisible();
		await expect(spark).toHaveAttribute('aria-hidden', 'true'); // decorative: the value is in words
		await expect(spark.locator('path.line')).toHaveAttribute('d', /^M/);
		const steps = page.getByRole('article', { name: 'Steps', exact: true });
		expect(await steps.locator('svg.spark rect.bar').count()).toBeGreaterThan(1);
	});
}
