// The Apple Watch views (J22.18) on the synthetic fakes of watch-fake.ts: ECG list and recording
// with its strip, beat-to-beat, activity rings, a workout's route and segments, the event families
// and the Explore entries. Each view also with its empty state. Values are shown as recorded; the
// copy review leaves only Apple's own ECG classification words.
import { classificationWords } from '../src/lib/watch/watch.ts';
import { test as dataTest } from './explore-fake';
import { beatsGap, ecgIds, expect, pausedDay, routeCount, routeInvalid, test } from './watch-fake';

test('ECG: every recording with Apple’s classification as recorded, newest first', async ({ page }) => {
	await page.goto('/explore/ecg');
	await expect(page).toHaveTitle('ECG · Vitamux');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('ECG');
	await expect(page.getByRole('heading', { name: '3 recordings' })).toBeVisible();
	const rows = page.getByRole('table').getByRole('row');
	await expect(rows).toHaveCount(4);
	await expect(rows.nth(1)).toContainText(/Sinus rhythm\s*64 bpm\s*None recorded\s*Apple Health · Watch/);
	await expect(rows.nth(2)).toContainText(/Inconclusive: poor reading\s*Not recorded\s*Not set/);
	await expect(rows.nth(3)).toContainText(/Atrial fibrillation\s*88 bpm\s*Recorded as present/);
	// Nothing is coloured by result: every classification reads in the same ink.
	const colours = await page.locator('td.classification').evaluateAll((cells) => cells.map((c) => getComputedStyle(c).color));
	expect(new Set(colours).size).toBe(1);

	await rows.nth(1).getByRole('link').click();
	await expect(page).toHaveURL(`/explore/ecg/${ecgIds.withWaveform}`);
});

test('ECG: the empty range says how recordings arrive', async ({ page, watch }) => {
	watch.empty = true;
	await page.goto('/explore/ecg');
	await expect(page.getByText('No ECG recordings in this range')).toBeVisible();
	await expect(page.getByText(/Turn on the ECG group/)).toBeVisible();
});

test('ECG recording: the facts as recorded and the strip at 25 mm/s and 10 mm/mV, scrollable, with a table', async ({ page }) => {
	await page.goto(`/explore/ecg/${ecgIds.withWaveform}`);
	await expect(page).toHaveTitle('ECG recording · Vitamux');
	await expect(page.getByRole('heading', { name: 'Sinus rhythm' })).toBeVisible();
	await expect(page.getByText('Classification recorded by Apple’s ECG app, shown as recorded')).toBeVisible();
	const facts = page.locator('dl');
	await expect(facts.locator('div').filter({ hasText: 'Average heart rate' })).toContainText('64 bpm');
	await expect(facts.locator('div').filter({ hasText: 'Symptoms' })).toContainText('None recorded');
	await expect(facts.locator('div').filter({ hasText: 'Sampling frequency' })).toContainText('512 Hz');
	await expect(facts.locator('div').filter({ hasText: 'Lead' })).toContainText('Apple Watch, similar to lead I');
	await expect(facts.locator('div').filter({ hasText: /^Recorded/ })).toContainText('08:05');

	// 30 s at 25 mm/s and 4 px/mm is 3,000 px: wider than the card, so the region scrolls sideways.
	const region = page.getByRole('region', { name: 'ECG waveform at 25 mm/s and 10 mm/mV, scrolls sideways' });
	await expect(region.getByRole('img', { name: /30 s at 512 Hz, from -?[\d.]+ to [\d.]+ mV/ })).toBeVisible();
	expect(await region.locator('svg').getAttribute('width')).toBe('3000');
	expect(await region.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true);
	await region.focus();
	await page.keyboard.press('ArrowRight');
	await expect.poll(() => region.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0);
	await expect(page.getByText(/each small square is 0\.04 s by 0\.1 mV/)).toBeVisible();

	await page.getByText('Show as a table').click();
	const table = page.getByRole('table', { name: /lowest and highest value per second/ });
	await expect(table.getByRole('row')).toHaveCount(31);
	await expect(table.getByRole('row').nth(1)).toContainText('0–1 s');
});

test('ECG recording: no waveform, and an unknown id', async ({ page }) => {
	await page.goto(`/explore/ecg/${ecgIds.noWaveform}`);
	await expect(page.getByRole('heading', { name: 'Inconclusive: poor reading' })).toBeVisible();
	await expect(page.locator('dl div').filter({ hasText: 'Average heart rate' })).toContainText('Not recorded');
	await expect(page.getByText('No waveform is stored for this recording.')).toBeVisible();
	await expect(page.getByRole('region', { name: /scrolls sideways/ })).toHaveCount(0);

	await page.goto('/explore/ecg/00000000-0000-4000-8000-000000000999');
	await expect(page.getByText('No ECG recording with this id')).toBeVisible();
});

test('beat-to-beat: one RR chart per series, a day stepper, and a day without beats', async ({ page }) => {
	await page.goto('/explore/beats?date=2026-09-14');
	await expect(page).toHaveTitle('Beat-to-beat · Vitamux');
	await expect(page.getByRole('heading', { name: /^Series 1 · 07:00–07:01$/ })).toBeVisible();
	await expect(page.getByRole('heading', { name: /^Series 2 · 14:30–14:31$/ })).toBeVisible();
	await expect(page.getByText('89 intervals · Apple Health · Watch')).toHaveCount(2);
	await expect(page.getByRole('group', { name: /^RR intervals of series 1/ })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Next day' })).toBeDisabled();
	await page.getByRole('region', { name: /^Series 1/ }).getByText('Show as a table').click();
	await expect(page.getByRole('table', { name: 'RR intervals of series 1' }).getByRole('row').nth(1)).toContainText(/07:01.*\d{3}$/);

	await page.getByRole('button', { name: 'Previous day' }).click();
	await expect(page).toHaveURL(/\?date=2026-09-13/);
	await expect(page.getByRole('heading', { name: /^Series 1/ })).toBeVisible();

	await page.goto(`/explore/beats?date=${beatsGap}`);
	await expect(page.getByText('No beat-to-beat series on this day')).toBeVisible();
});

test('activity rings: each day against Apple’s goal, a paused day, and an empty range', async ({ page, watch }) => {
	await page.goto('/explore/activity-rings');
	await expect(page).toHaveTitle('Activity rings · Vitamux');
	await expect(page.getByRole('heading', { name: '7 days' })).toBeVisible();
	await expect(page.getByText('Goals are Apple’s, as set in the Activity app on each day. Apple Health · Activity summary.')).toBeVisible();
	const rows = page.getByRole('table').getByRole('row');
	await expect(rows).toHaveCount(8);
	await expect(page.getByRole('columnheader', { name: 'Move of Apple’s goal' })).toBeVisible();
	await expect(rows.nth(1)).toContainText(/\d+ kcal of 500 kcal\s*\d+ min of 30 min\s*\d+ of 12 hours/);
	const paused = page.getByRole('row', { name: new RegExp(pausedDay.slice(-2)) }).filter({ hasText: 'Rings paused' });
	await expect(paused).toContainText('0 kcal of 500 kcal');

	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '3M' }).click();
	await expect(page.getByRole('heading', { name: '60 days' })).toBeVisible();

	watch.empty = true;
	await page.getByRole('group', { name: 'Range' }).getByRole('button', { name: '1M' }).click();
	await expect(page.getByText('No activity summaries in this range')).toBeVisible();
});

test('workout: the route as a plain path without tiles, laps, a pause and a marker', async ({ page }) => {
	const hosts = new Set<string>();
	page.on('request', (r) => hosts.add(new URL(r.url()).host));
	await page.goto('/explore/workouts');
	await page.getByRole('link', { name: 'Route and laps of Apple Health workout' }).click();
	await expect(page).toHaveURL('/explore/workouts/w-run-apple');
	await expect(page).toHaveTitle('Running · Vitamux');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Running');
	await expect(page.locator('dl div').filter({ hasText: 'Distance' })).toContainText('8 km');

	const valid = (routeCount - routeInvalid).toLocaleString('en-US');
	await expect(page.getByRole('img', { name: new RegExp(`Route of this workout: ${valid} locations as recorded, drawn without a map`) })).toBeVisible();
	await expect(page.getByText(/the panel loads no map tiles/)).toBeVisible();
	await expect(page.getByRole('group', { name: /^Segments over the workout/ })).toBeVisible();
	const segments = page.getByRole('region', { name: 'Laps and activities' }).getByRole('table').last();
	await expect(segments.getByRole('row')).toHaveCount(8);
	// In time order: a lap every 8 min, the pause at 19 min, the marker at 30 min.
	await expect(segments.getByRole('rowheader')).toHaveText(['Lap 1', 'Lap 2', 'Lap 3', 'Pause 1', 'Lap 4', 'Marker 1', 'Lap 5']);
	await page.getByRole('region', { name: 'Route' }).getByText('Show as a table').click();
	await expect(page.getByRole('table', { name: /Route of this workout, locations/ }).getByRole('row').nth(1)).toContainText('0.01000');
	// Nothing but the panel itself was asked for anything: no map tiles, no third party.
	expect([...hosts]).toEqual(['127.0.0.1:4173']);
});

test('workout: activities without a route, a workout without segments, an unknown id', async ({ page }) => {
	await page.goto('/explore/workouts/w-ride');
	await expect(page.getByText('No route was recorded for this workout.')).toBeVisible();
	const segments = page.getByRole('region', { name: 'Laps and activities' }).getByRole('table').last();
	await expect(segments.getByRole('row', { name: /Cycling/ })).toContainText(/30 min\s*11\.2 km\s*290 kcal\s*Outdoor/);
	await expect(segments.getByRole('row', { name: /Running/ })).toContainText('2.4 km');

	await page.goto('/explore/workouts/w-walk');
	await expect(page.getByText('No laps, activities, pauses or markers were recorded.')).toBeVisible();
	await expect(page.getByText('No route was recorded for this workout.')).toBeVisible();

	await page.goto('/explore/workouts/w-none');
	await expect(page.getByText('No workout with this id')).toBeVisible();
});

test('events: lanes and the type picker grouped by family, with the new families', async ({ page }) => {
	await page.goto('/explore/events');
	await expect(page.getByRole('heading', { name: '11 events in 8 types' })).toBeVisible();
	await expect(page.getByRole('heading', { level: 3 })).toHaveText(['Heart rhythm and rate', 'Mind', 'Cycle tracking', 'Symptoms', 'Other alerts']);
	const heart = page.getByRole('region', { name: 'Heart rhythm and rate' });
	await expect(heart.getByRole('link', { name: 'ECG recordings and their strips' })).toHaveAttribute('href', '/explore/ecg');
	await heart.getByText('Show as a table').click();
	const lanes = heart.getByRole('table');
	await expect(lanes.getByRole('row', { name: /Atrial fibrillation/ })).toContainText('Ecg recording');
	await expect(lanes.getByRole('row', { name: /Irregular rhythm alert/ })).toHaveCount(1);
	await expect(page.getByRole('region', { name: 'Mind' }).getByRole('group', { name: /^Events by type over time: Mind/ })).toBeVisible();
	const groups = await page.getByLabel('Event type').locator('optgroup').evaluateAll((g) => g.map((x) => x.getAttribute('label')));
	expect(groups).toEqual(['Heart rhythm and rate', 'Mind', 'Cycle tracking', 'Symptoms', 'Other alerts']);

	await page.getByLabel('Event type').selectOption('symptom_headache');
	await expect(page.getByRole('heading', { name: '1 event in 1 type' })).toBeVisible();
	await expect(page.getByRole('heading', { level: 3 })).toHaveText(['Symptoms']);
});

test('Explore lists the new quantities from the catalogue and opens their views', async ({ page }) => {
	await page.goto('/explore');
	await expect(page.getByRole('link', { name: 'Rr interval' })).toHaveAttribute('href', '/explore/rr_interval');
	await expect(page.getByRole('region', { name: 'Activity' }).getByRole('link', { name: 'Stand hours' })).toHaveAttribute('href', '/explore/stand_hours');
	await expect(page.getByRole('link', { name: 'Ecg recording' })).toHaveAttribute('href', '/explore/ecg');
	await expect(page.getByRole('link', { name: 'State of mind' })).toHaveAttribute('href', '/explore/events?code=state_of_mind');

	await page.goto('/explore/stand_hours');
	await expect(page.getByRole('link', { name: 'activity rings view' })).toHaveAttribute('href', '/explore/activity-rings');
	await page.goto('/explore/rr_interval?end=2026-09-14');
	await expect(page.getByRole('link', { name: 'beat-to-beat view' })).toHaveAttribute('href', '/explore/beats?date=2026-09-14');
});

dataTest('an HRV day opens its beat-to-beat series from the point panel', async ({ page, data }) => {
	await page.route('**/api/v1/metrics/hrv_rmssd', (r) =>
		r.fulfill({ json: { code: 'hrv_rmssd', section: 'Heart and circulation', unit: 'ms', kinds: ['sample'], aggregation: 'intensive', windows: ['local_day'], strategies: ['first_available'], plausible_range: [1, 500], provider_scoped: false, selection_only: false } })
	);
	const resolve = data.resolve.bind(data);
	data.resolve = (m, date) =>
		m === 'hrv_rmssd'
			? { status: 'direct', value: 42, unit: 'ms', window: { kind: 'local_day', local_date: date }, rule: { ref: 'builtin:hrv_rmssd', version: 1, strategy: 'first_available' }, inputs: [], explanation: 'First available source: Apple Watch 42 ms.' }
			: resolve(m, date);
	await page.goto('/explore/hrv_rmssd?range=1W&end=2026-09-16');
	await page.getByRole('group', { name: /resolved per day/ }).focus();
	await page.keyboard.press('Enter');
	const panel = page.getByRole('region', { name: /Sep 16, 2026/ });
	await expect(panel.getByRole('link', { name: 'Beat-to-beat intervals on this day' })).toHaveAttribute('href', '/explore/beats?date=2026-09-16');
});

// Nothing rates an ECG, rhythm, cycle or mood value (CLAUDE.md hard rules). Apple's ECG
// classification is shown word for word, so its phrases are the only exception.
const judgement = /\b(good|bad|poor|excellent|great|healthy|unhealthy|normal|abnormal|optimal|ideal|elevated|dangerous|concerning|warning|(high|low) risk)\b/i;
const pages = ['/explore/ecg', `/explore/ecg/${ecgIds.withWaveform}`, `/explore/ecg/${ecgIds.noWaveform}`, '/explore/beats?date=2026-09-14', '/explore/activity-rings', '/explore/workouts/w-run-apple', '/explore/workouts/w-ride', '/explore/events'];

test('no Watch view contains a judgement word', async ({ page }) => {
	for (const path of pages) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await page.waitForLoadState('networkidle');
		await page.evaluate(() => document.querySelectorAll('details').forEach((d) => (d.open = true)));
		let text = await page.locator('main').innerText();
		for (const words of classificationWords) text = text.replaceAll(words, '');
		expect(text.match(judgement)?.[0], `${path} contains a judgement word`).toBeUndefined();
	}
});

test('the Watch views fit a phone: no sideways page scrolling at 390 px', async ({ page }) => {
	await page.setViewportSize({ width: 390, height: 844 });
	for (const path of pages) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await page.waitForLoadState('networkidle');
		const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
		expect(overflow, `${path} scrolls sideways`).toBeLessThanOrEqual(0);
	}
});
