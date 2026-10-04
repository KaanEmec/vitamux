// The chart kit on the specialised views (J23.4): stage stacks (Bars), RangeBars, Hypnogram,
// RangeDumbbell, a step TimeSeries and EventLanes, in both themes, from the keyboard and as
// tables. Synthetic data (views-fake.ts).
import { schemes, walk } from './charts';
import { expect, test } from './views-fake';

for (const scheme of schemes) {
	test(`sleep: Bars stacked by stage, RangeBars and Hypnogram (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/sleep');
		const stacks = page.getByRole('group', { name: /Sleep stages per night, stacked/ });
		await expect(stacks.locator('rect.bar.deep').first()).toBeVisible();
		const { last } = await walk(page, stacks, { rows: 30 });
		expect(last).toMatch(/Deep/);

		const bed = page.getByRole('group', { name: /Bed and wake time per night/ });
		await expect(bed.locator('rect.bar.picked')).toHaveCount(1);
		await expect(bed.locator('.mean')).toHaveCount(2);
		await walk(page, bed, { rows: 30 });

		const hyp = page.getByRole('group', { name: /Sleep stages of Apple Health · Apple Watch/ });
		const { first } = await walk(page, hyp);
		expect(first).toMatch(/Light\s*\d+ min/);
	});

	test(`blood pressure: RangeDumbbell and a step TimeSeries (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/blood-pressure');
		const bp = page.getByRole('group', { name: /Systolic and diastolic readings/ });
		const { last } = await walk(page, bp);
		expect(last).toMatch(/\d+\/\d+\s*mmHg/);
		await walk(page, page.getByRole('group', { name: /Pulse of the same readings/ }));
	});

	test(`events: EventLanes (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/events');
		const lanes = page.getByRole('group', { name: /^Events by type over time/ });
		await expect(lanes.locator('rect.event')).toHaveCount(3);
		await walk(page, lanes, { rows: 3 });
	});
}
