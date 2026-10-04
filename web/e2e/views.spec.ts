import { expect, test } from './views-fake';

test('sleep: last night, stage stacks, bed and wake, the average night and the nights list', async ({ page }) => {
	await page.goto('/explore/sleep');
	await expect(page).toHaveTitle('Sleep · Vitamux');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sleep');
	await expect(page.getByRole('button', { name: '30 nights' })).toHaveAttribute('aria-pressed', 'true');

	// Last night: duration, time in bed, efficiency, bedtime and wake from the episode, minutes per stage and the sources.
	const hero = page.getByRole('region', { name: /^Last night · .*14 Sep/ });
	await expect(hero.locator('.big')).toHaveText('7h 19m');
	const facts = hero.locator('dl.facts');
	await expect(facts.locator('div').filter({ hasText: 'In bed' })).toContainText('7h 44m');
	await expect(facts.locator('div').filter({ hasText: 'Efficiency' })).toContainText('95%');
	await expect(facts.locator('div').filter({ hasText: 'Bedtime' })).toContainText('23:23');
	await expect(facts.locator('div').filter({ hasText: 'Wake' })).toContainText('07:07');
	await expect(hero.locator('.stages li').filter({ hasText: 'Deep' })).toContainText('1h 30m');
	await expect(hero.locator('.stages li').filter({ hasText: 'Awake' })).toContainText('0h 25m');
	await expect(hero.getByText('Apple Health · Apple Watch · used')).toBeVisible();
	await expect(hero.getByText(/WHOOP · Band · \d+h \d{2}m/)).toBeVisible();
	await expect(hero.getByRole('group', { name: /Sleep stages of Apple Health · Apple Watch/ })).toBeVisible();

	await expect(page.getByRole('group', { name: /Sleep stages per night, stacked/ })).toBeVisible();
	await expect(page.getByRole('group', { name: /Bed and wake time per night/ })).toBeVisible();
	await expect(page.getByText(/Median bedtime\s*\d\d:\d\d/)).toBeVisible();
	await expect(page.getByText(/Median wake\s*\d\d:\d\d/)).toBeVisible();
	const average = page.getByRole('region', { name: 'Average night' });
	await expect(average).toContainText('29 of 30 nights'); // one night has no episode
	await expect(average.getByRole('img', { name: 'Average night by stage' })).toBeVisible();
	await expect(average.getByRole('listitem')).toHaveCount(4);

	// Every source of last night, with where each stands under the rule; the selected one is the hero.
	const across = page.getByRole('region', { name: /Sources for the night of .*14.*2026/ });
	const watch = across.getByRole('article', { name: 'Apple Health · Apple Watch' });
	await expect(watch.getByText('Selected')).toBeVisible();
	const band = across.getByRole('article', { name: 'WHOOP · Band' });
	await expect(band.getByText('In the rule')).toBeVisible();
	await expect(band.getByRole('group', { name: /Sleep stages of WHOOP · Band/ })).toBeVisible();
	const relay = across.getByRole('article', { name: 'Apple Health · Garmin Connect' });
	await expect(relay.getByText('Excluded: exclude: relayed=true')).toBeVisible();
	await expect(relay.getByText('This source reported no sleep stages.')).toBeVisible();
	await expect(across.getByText('Naps')).toBeHidden();

	const opener = watch.getByRole('button', { name: 'Provenance of Apple Health · Apple Watch sleep' });
	await opener.click();
	const dialog = page.getByRole('dialog', { name: /Provenance of sleep/ });
	await expect(dialog).toBeVisible();
	await dialog.getByRole('button', { name: 'Close' }).click();
	await expect(dialog).toBeHidden();
	await expect(opener).toBeFocused(); // focus returns to the button that opened it

	// The list: the newest nights first; a row opens to the time per stage and every source, and shows that night above.
	const list = page.getByRole('region', { name: 'Nights' });
	await expect(list.locator('summary')).toHaveCount(7);
	await list.getByRole('button', { name: /Show more \(22 left\)/ }).click();
	await expect(list.locator('summary')).toHaveCount(14);
	const row = list.locator('details').nth(1);
	await row.locator('summary').click();
	await expect(row.getByText(/Selected · 7h/)).toBeVisible();
	await expect(row.getByText(/In the rule · /)).toBeVisible();
	await row.getByRole('button', { name: /Show this night/ }).click();
	await expect(page.getByRole('region', { name: /^Night · Sat 12 → Sun 13 Sep/ })).toBeVisible();
	const napped = page.getByRole('region', { name: /Sources for the night of .*13.*2026/ });
	await expect(napped.getByRole('heading', { name: 'Naps' })).toBeVisible();
	await expect(napped.getByRole('listitem').filter({ hasText: '14:10 to 14:50' })).toContainText('0:40');
	await expect(napped.getByRole('article')).toHaveCount(2);

	// A chart picks a night from the keyboard: back to last night.
	const stacks = page.getByRole('group', { name: /Sleep stages per night, stacked/ });
	await stacks.focus();
	await page.keyboard.press('End');
	await page.keyboard.press('Enter');
	await expect(page.getByRole('region', { name: /^Last night/ })).toBeVisible();

	await page.getByRole('button', { name: '7 nights' }).click();
	await expect(average).toContainText('7 of 7 nights');
});

test('blood pressure: sessions of readings, a morning and evening filter, and the readings table', async ({ page }) => {
	await page.goto('/explore/blood-pressure');
	await expect(page).toHaveTitle('Blood pressure · Vitamux');
	await expect(page.getByRole('button', { name: '90D' })).toHaveAttribute('aria-pressed', 'true');
	const stats = page.locator('dl');
	await expect(stats.locator('div').filter({ hasText: '90-day mean' })).toContainText(/\d+\/\d+\s*mmHg/);
	await expect(stats.locator('div').filter({ hasText: 'Readings' })).toContainText('22');
	await expect(page.getByRole('group', { name: /Systolic and diastolic readings/ })).toBeVisible();
	await expect(page.getByRole('group', { name: /Pulse of the same readings/ })).toBeHidden(); // pulse is in the tooltip
	await expect(page.getByText(/Morning is before 12:00 and evening from 17:00/)).toBeVisible();

	const table = page.getByRole('table').last();
	await expect(table.getByRole('row')).toHaveCount(23);
	const newest = table.getByRole('row').nth(1);
	await expect(newest).toContainText('2026-09-14 07:20');
	await expect(newest).toContainText('Position seated');
	await expect(newest).toContainText('Withings');
	await expect(table.getByRole('row', { name: /Manual entry/ })).toHaveCount(1);

	await page.getByRole('group', { name: 'Time of day' }).getByRole('button', { name: 'Evening' }).click();
	await expect(stats.locator('div').filter({ hasText: 'Readings' })).toContainText('1');
	await expect(table.getByRole('row')).toHaveCount(2);
	await expect(table.getByRole('row').nth(1)).toContainText('2026-09-10 19:30');
	await page.getByRole('group', { name: 'Time of day' }).getByRole('button', { name: 'Morning' }).click();
	await expect(table.getByRole('row')).toHaveCount(22);
	await page.getByRole('button', { name: '30D' }).click();
	await expect(page.getByText('30-day mean')).toBeVisible();
});

test('body composition: weight with its 7-day average, and a tile for each other part', async ({ page }) => {
	await page.goto('/explore/body-composition');
	await expect(page).toHaveTitle('Body composition · Vitamux');
	await expect(page.getByText('Latest · 2026-09-14')).toBeVisible();
	await expect(page.locator('dl div').filter({ hasText: 'Latest ·' })).toContainText('78.2');
	await expect(page.locator('dl div').filter({ hasText: 'Change in range' })).toContainText('−1.8');
	await expect(page.getByRole('group', { name: /Weight per weigh-in/ })).toBeVisible();

	const tiles = page.getByRole('region', { name: 'Body composition' }).getByRole('listitem');
	await expect(tiles).toHaveCount(4);
	const fat = tiles.filter({ hasText: 'Fat mass' });
	await expect(fat).toContainText('−0.9 kg since 2026-08-18');
	await expect(fat.locator('svg.spark')).toBeVisible();
	await expect(fat.getByText('Withings · Scale')).toBeVisible();
	await expect(tiles.filter({ hasText: 'Bone mass' })).toContainText('No change since 2026-08-18');
	await expect(page.getByText(/\b(BMI|underweight|overweight|obese)\b/i)).toHaveCount(0); // no classes

	await expect(page.getByRole('columnheader', { name: 'Fat-free mass (kg)' })).toBeVisible();
	await expect(page.getByRole('columnheader', { name: 'Bone mass (kg)' })).toBeVisible();
	await expect(page.getByRole('table').last().getByRole('row')).toHaveCount(12);
});

test('workouts: a calendar and clusters with every source', async ({ page }) => {
	await page.goto('/explore/workouts');
	await expect(page).toHaveTitle('Workouts · Vitamux');
	await expect(page.getByRole('heading', { name: 'September 2026' })).toBeVisible();
	await expect(page.getByRole('button', { name: '2026-09-14, 2 workouts' })).toBeVisible();

	const run = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: /Running/ }) });
	await expect(run.getByText('2 sources')).toBeVisible();
	await expect(run.getByRole('row', { name: /Garmin · Forerunner/ })).toContainText('Selected');
	await expect(run.getByRole('row', { name: /Apple Health/ })).toContainText('Not in the rule');
	await expect(run.getByText('Used garmin')).toBeVisible();
	await expect(page.getByRole('heading', { name: /Cycling/ })).toBeVisible();

	await run.getByRole('button', { name: 'Provenance of Garmin · Forerunner workout' }).click();
	const dialog = page.getByRole('dialog', { name: /Provenance of workout/ });
	await expect(dialog).toBeVisible();
	await dialog.getByRole('button', { name: 'Close' }).click();
	await expect(dialog).toBeHidden();

	// Picking a day lists that day only; picking it again clears.
	await page.getByRole('button', { name: '2026-09-10, 1 workout' }).click();
	await expect(page.getByRole('heading', { name: /Walking/ })).toBeVisible();
	await expect(page.getByRole('heading', { name: /Running/ })).toBeHidden();
	await page.getByRole('button', { name: '2026-09-10, 1 workout' }).click();
	await expect(page.getByRole('heading', { name: /Running/ })).toBeVisible();

	await page.getByRole('button', { name: 'Next month' }).click();
	await expect(page.getByText('No workouts in this month')).toBeVisible();
});

test('events: one lane per type, and a single type with ?code=', async ({ page, views }) => {
	await page.goto('/explore/events');
	await expect(page).toHaveTitle('Events · Vitamux');
	await expect(page.getByRole('heading', { name: '3 events in 2 types' })).toBeVisible();
	await expect(page.getByRole('group', { name: /^Events by type over time/ })).toBeVisible();
	await expect(page.getByRole('group', { name: /^Events by type over time/ }).getByText('Environment audio alert')).toBeVisible();

	await page.getByLabel('Event type').selectOption('headphone_audio_alert');
	await expect(page).toHaveURL(/\?code=headphone_audio_alert/);
	await expect(page.getByRole('heading', { name: '1 event in 1 type' })).toBeVisible();
	expect(views.eventQueries.at(-1)).toContain('code=headphone_audio_alert');

	await page.goto('/explore/events?code=environment_audio_alert');
	await expect(page.getByRole('heading', { name: '2 events in 1 type' })).toBeVisible();
	await expect(page.getByLabel('Event type')).toHaveValue('environment_audio_alert');
});

test('lab analyte: results as printed, with the printed range and no added flags', async ({ page }) => {
	await page.goto('/lab/analytes/glucose');
	await expect(page).toHaveTitle('Glucose · Lab results · Vitamux');
	await expect(page.getByRole('heading', { name: /^Glucose/ })).toBeVisible();
	await expect(page.getByText('Latest result · 2026-08-30')).toBeVisible();

	// The latest result is printed in mg/dL: that unit is plotted first, the others stay in the table.
	const units = page.getByRole('group', { name: 'Printed unit' });
	await expect(units.getByRole('button', { name: 'mg/dL' })).toHaveAttribute('aria-pressed', 'true');
	await expect(page.getByText('Plotted in mg/dL, as printed.')).toBeVisible();
	await units.getByRole('button', { name: 'mmol/L' }).click();
	await expect(page.getByText(/Plotted in mmol\/L, as printed\..*reference range printed on each report/)).toBeVisible();
	await expect(page.getByRole('group', { name: /Glucose results over time/ })).toBeVisible();

	const table = page.getByRole('table').last();
	await expect(table.getByRole('row')).toHaveCount(5);
	const low = table.getByRole('row', { name: /2026-03-05/ });
	await expect(low).toContainText('< 0.5');
	await expect(low).toContainText('3.9 - 5.5');
	await expect(low.getByRole('cell', { name: 'L', exact: true })).toBeVisible();
	await expect(table.getByRole('row', { name: /2025-03-10/ }).getByRole('cell', { name: '–' }).first()).toBeVisible(); // no printed flag

	await page.goto('/lab/analytes/creatinine');
	await expect(page.getByRole('group', { name: 'Printed unit' })).toBeHidden();
	await page.goto('/lab/analytes/nothing');
	await expect(page.getByText('No confirmed results for this analyte')).toBeVisible();
});

// The views never judge a value (CLAUDE.md hard rules). Lab values, ranges and flags are shown as printed.
const judgement = /\b(good|bad|poor|excellent|great|healthy|unhealthy|normal|abnormal|optimal|ideal|elevated|dangerous|concerning|warning|(high|low) risk)\b/i;
const pages = ['/explore/sleep', '/explore/blood-pressure', '/explore/body-composition', '/explore/workouts', '/explore/events', '/lab/analytes/glucose'];

test('no page of the views contains a judgement word', async ({ page }) => {
	for (const path of pages) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await page.waitForLoadState('networkidle');
		// The table fallbacks are part of the page: open them before reading.
		await page.evaluate(() => document.querySelectorAll('details').forEach((d) => (d.open = true)));
		const text = await page.locator('main').innerText();
		expect(text.match(judgement)?.[0], `${path} contains a judgement word`).toBeUndefined();
	}
});

test('the views fit a phone: no sideways scrolling at 390 px', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	for (const path of pages) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await page.waitForLoadState('networkidle');
		const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
		expect(overflow, `${path} scrolls sideways`).toBeLessThanOrEqual(0);
	}
});
