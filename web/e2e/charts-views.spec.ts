// The chart kit on the specialised views (J23.4): stage stacks (Bars), RangeBars, Hypnogram,
// RangeDumbbell, a dots-and-trend TimeSeries and EventLanes, in both themes, from the keyboard and as
// tables. Synthetic data (views-fake.ts).
import { schemes, walk } from './charts';
import { expect, test } from './views-fake';

for (const scheme of schemes) {
	test(`sleep: Bars stacked by stage, RangeBars with bands and Hypnogram (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/sleep');
		const stacks = page.getByRole('group', { name: /Sleep stages per night, stacked/ });
		await expect(stacks.locator('rect.bar.deep').first()).toBeVisible();
		await expect(stacks.locator('rect.picked')).toHaveCount(1); // the shown night is outlined
		const { last } = await walk(page, stacks, { rows: 30 });
		expect(last).toMatch(/Deep/);

		const bed = page.getByRole('group', { name: /Bed and wake time per night/ });
		await expect(bed.locator('rect.bar.picked')).toHaveCount(1);
		await expect(bed.locator('rect.band')).toHaveCount(2); // the middle half of bed times and of wake times
		await expect(bed.locator('.mean')).toHaveCount(0);
		await walk(page, bed, { rows: 30 });

		const hyp = page.getByRole('group', { name: /Sleep stages of Apple Health · Apple Watch/ });
		const { first } = await walk(page, hyp);
		expect(first).toMatch(/Light\s*\d+ min/);
	});

	test(`blood pressure: RangeDumbbell with pulse, context and source in the tooltip (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/blood-pressure');
		const bp = page.getByRole('group', { name: /Systolic and diastolic readings/ });
		await expect(bp.locator('circle.lo')).toHaveCount(21); // 22 readings; the newest two are one session
		const { last } = await walk(page, bp, { rows: 21 });
		expect(last).toMatch(/\d+\/\d+\s*mmHg/);
		expect(last).toMatch(/Pulse\s*\d+ bpm/);
		expect(last).toMatch(/Position seated/);
		expect(last).toMatch(/Mean of 2 readings within 30 minutes/);
	});

	test(`body: weight dots with a 7-day average line (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/body-composition');
		const weight = page.getByRole('group', { name: /Weight per weigh-in/ });
		await expect(weight.locator('circle.dot')).toHaveCount(11);
		await expect(weight.locator('path.line.trend')).toHaveCount(1);
		const { last } = await walk(page, weight);
		expect(last).toMatch(/7-day average/);
	});

	test(`events: EventLanes (${scheme})`, async ({ page }) => {
		await page.emulateMedia({ colorScheme: scheme });
		await page.goto('/explore/events');
		const lanes = page.getByRole('group', { name: /^Events by type over time/ });
		await expect(lanes.locator('rect.event')).toHaveCount(3);
		await walk(page, lanes, { rows: 3 });
	});
}
