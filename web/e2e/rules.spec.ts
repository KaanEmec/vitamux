import { readFileSync } from 'node:fs';
import type { Locator, Page } from '@playwright/test';
import { expect, test } from './rules-fake';

type Json = Record<string, unknown>;
type Sel = Record<string, string | boolean>;

// The four CLAUDE.md example rules, as validated by internal/resolve.
const example = (name: string): Json =>
	JSON.parse(readFileSync(new URL(`../../internal/resolve/testdata/valid/${name}.json`, import.meta.url), 'utf8'));

/** Drops empty arrays (`acknowledged_warnings: []` means the same as leaving it out). */
function normalize(v: unknown): unknown {
	if (Array.isArray(v)) return v.map(normalize);
	if (v && typeof v === 'object') {
		return Object.fromEntries(
			Object.entries(v)
				.filter(([, x]) => !(Array.isArray(x) && x.length === 0))
				.map(([k, x]) => [k, normalize(x)])
		);
	}
	return v;
}

const valueLabel: Record<string, string> = {
	true: 'relayed by another app',
	false: 'not relayed (direct)',
	manual: 'entered manually',
	device: 'measured by a device'
};

/** Fills the selectors of a SelectorList inside `scope` (kind "Match" or "Exclusion"). */
async function fillSelectors(scope: Locator, kind: string, selectors: Sel[], startEmpty: boolean) {
	for (const [si, sel] of selectors.entries()) {
		if (si > 0 || startEmpty) {
			await scope.getByRole('button', { name: si === 0 ? `Add ${kind.toLowerCase()}` : `Or ${kind.toLowerCase()}…` }).click();
		}
		const s = scope.getByRole('group', { name: `${kind} ${si + 1}`, exact: true });
		for (const [ci, [field, value]] of Object.entries(sel).entries()) {
			if (ci > 0) await s.getByRole('button', { name: 'And…' }).click();
			await s.getByLabel(`Condition ${ci + 1} field`).selectOption(field);
			const input = s.getByLabel(`Condition ${ci + 1} value`);
			if (field === 'relayed' || field === 'entry') await input.selectOption({ label: valueLabel[String(value)] });
			else await input.fill(String(value));
		}
	}
}

/** Builds `rule` from an empty rule in the five steps and returns at the review step. */
async function build(page: Page, rule: Json, opts: { acknowledge?: boolean } = {}) {
	await page.goto('/rules/new');
	await page.getByLabel('Metric', { exact: true }).selectOption(rule.metric as string);
	await page.getByLabel('An empty rule').check();
	await page.getByRole('button', { name: 'Next' }).click();

	// 2. Sources
	await expect(page.getByRole('heading', { name: '2. Sources' })).toBeFocused();
	const groups = rule.groups as { id: string; match: Sel[] }[];
	for (const [i, g] of groups.entries()) {
		if (i > 0) await page.getByRole('button', { name: 'Add group' }).click();
		const fs = page.getByRole('group', { name: `Group ${i + 1}`, exact: true });
		await fs.getByLabel('Group id').fill(g.id);
		await fillSelectors(fs, 'Match', g.match, false);
	}
	if (rule.exclude) {
		await fillSelectors(page.getByRole('group', { name: 'Exclusions' }), 'Exclusion', rule.exclude as Sel[], true);
	}
	await page.getByRole('button', { name: 'Next' }).click();

	// 3. Strategy
	const strategy = rule.strategy as Json;
	const ws = (rule.within_source ?? {}) as Json;
	const opName: Record<string, RegExp> = {
		first_available: /^Use the first source with data/,
		mean_across_sources: /^Average the sources/,
		sum_across_sources: /^Add the sources together/
	};
	await page.getByRole('radio', { name: opName[strategy.op as string] }).check();
	if (strategy.min_sources) await page.getByLabel('Minimum sources').fill(String(strategy.min_sources));
	if (strategy.on_insufficient) await page.getByLabel('With fewer sources').selectOption(strategy.on_insufficient as string);
	if (ws.intra_group) await page.getByLabel('Several sources in one group').selectOption(ws.intra_group as string);
	if (ws.daily_value_policy) await page.getByLabel('Daily totals').selectOption(ws.daily_value_policy as string);
	if (opts.acknowledge) await page.getByLabel('I understand the duplicate risk').check();
	await page.getByRole('button', { name: 'Next' }).click();

	// 4. Window and quality
	const w = rule.window as Json;
	await page.getByLabel('Window', { exact: true }).selectOption(w.kind as string);
	if (w.size) await page.getByLabel('Bucket size').selectOption(w.size as string);
	const q = (rule.quality ?? {}) as Json;
	if (q.min_coverage) await page.getByLabel('Minimum coverage').fill(String(q.min_coverage));
	if (q.plausible_range) {
		const [low, high] = q.plausible_range as number[];
		await page.getByLabel('Plausible low').fill(String(low));
		await page.getByLabel('Plausible high').fill(String(high));
	}
	if (q.max_staleness) await page.getByLabel('Maximum staleness').fill(q.max_staleness as string);
	for (const f of (q.exclude_flags ?? []) as string[]) await page.getByRole('checkbox', { name: f.replaceAll('_', ' ') }).check();
	const sleep = q.sleep as Json | undefined;
	if (sleep) {
		await page.getByText('Sleep alignment').click();
		await page.getByLabel('Episode match overlap').fill(String(sleep.match_overlap));
		await page.getByLabel('Minimum episode coverage').fill(String(sleep.min_episode_coverage));
		await page.getByLabel('Naps').selectOption(String(sleep.include_naps));
		await page.getByLabel('Night anchor').fill(sleep.night_anchor as string);
	}
	await page.getByRole('button', { name: 'Next' }).click();
	await expect(page.getByRole('heading', { name: '5. Review' })).toBeFocused();
}

test('the catalogue lists every metric with its rule, reason and coverage', async ({ page, rules }) => {
	rules.seedOwned('resting_heart_rate', {
		schema: 'vitamux.rule/1',
		metric: 'resting_heart_rate',
		window: { kind: 'local_day' },
		groups: [{ id: 'garmin', match: [{ provider: 'garmin' }] }],
		strategy: { op: 'first_available' }
	});
	await page.goto('/rules');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('Rules');
	await expect(page.getByText('Coverage heatmaps are not available yet.')).toBeVisible(); // GET /coverage 404s

	const hr = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'heart_rate', exact: true }) });
	await expect(hr.getByText('Built-in default')).toBeVisible();
	await expect(hr.getByText('Chest straps are ECG-class, then wrist devices by independent validation.')).toBeVisible();
	await expect(hr.getByText('chest_strap › apple_watch › garmin')).toBeVisible();
	const rhr = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'resting_heart_rate' }) });
	await expect(rhr.getByText('Your rule · version 2')).toBeVisible();
	const skin = page.getByRole('listitem').filter({ has: page.getByRole('heading', { name: 'skin_temperature' }) });
	await expect(skin.getByText('No rule')).toBeVisible();

	rules.coverage = 'ok';
	await page.reload();
	await expect(hr.getByRole('img', { name: 'garmin: data on 60 of 90 days' })).toBeVisible();
	await expect(hr.getByRole('img', { name: 'apple_watch: data on 30 of 90 days' })).toBeVisible();
	await expect(rhr.getByText('No data in the last 90 days.')).toBeVisible();
});

for (const name of ['claude-apple-watch-first', 'claude-heart-rate-mean', 'claude-resting-hr-first-available', 'claude-steps-sum']) {
	test(`builds the example rule ${name}`, async ({ page, rules }) => {
		const rule = example(name);
		await build(page, rule, { acknowledge: name === 'claude-steps-sum' });
		await expect(page.getByText('Preview unavailable')).toBeVisible(); // preview 404s until J10.3
		await page.getByRole('button', { name: 'Save version' }).click();
		await expect(page).toHaveURL(new RegExp(`/rules/${rule.metric}\\?saved=2$`));
		expect(rules.posted).toHaveLength(1);
		expect(normalize(rules.posted[0])).toEqual(normalize(rule));
	});
}

test('a sum cannot be saved without acknowledging the duplicate risk', async ({ page, rules }) => {
	await build(page, example('claude-steps-sum'));
	await page.getByRole('button', { name: 'Save version' }).click();

	await expect(page.getByRole('heading', { name: '3. Strategy' })).toBeFocused();
	await expect(page.getByText('Confirm that you understand the duplicate risk before saving a sum.')).toBeVisible();
	await expect(page.getByRole('button', { name: /Strategy/ }).getByRole('img', { name: 'has errors' })).toBeVisible();
	expect(rules.posted).toHaveLength(0);

	await page.getByLabel('I understand the duplicate risk').check();
	await page.getByRole('button', { name: /Review/ }).click();
	await page.getByRole('button', { name: 'Save version' }).click();
	await expect(page).toHaveURL('/rules/steps?saved=2');
	expect(rules.posted[0].acknowledged_warnings).toEqual(['cross_source_sum_duplicate_risk']);
});

test('preview shows the per-day differences from the active rule', async ({ page, rules }) => {
	rules.preview = 'ok';
	await page.goto('/rules/new?metric=heart_rate');
	await expect(page.getByRole('heading', { name: '2. Sources' })).toBeVisible();
	await expect(page.getByRole('group', { name: 'Group 1', exact: true }).getByLabel('Group id')).toHaveValue('chest_strap');
	await page.getByRole('button', { name: 'Move group 3 up' }).click();
	await expect(page.getByRole('group', { name: 'Group 2', exact: true }).getByLabel('Group id')).toHaveValue('garmin');
	await page.getByRole('button', { name: /Strategy/ }).click();
	await page.getByRole('radio', { name: /^Average the sources/ }).check();
	await page.getByRole('button', { name: /Review/ }).click();

	await expect(page.getByRole('status').filter({ hasText: 'days change' })).toHaveText('3 of 14 days change with this draft.');
	const table = page.getByRole('table');
	await expect(table.getByRole('row')).toHaveCount(15);
	const body = rules.previews[0] as { spec: Json; start_date: string; end_date: string };
	expect((body.spec.strategy as Json).op).toBe('mean_across_sources');
	expect((body.spec.groups as { id: string }[]).map((g) => g.id)).toEqual(['chest_strap', 'garmin', 'apple_watch']);
	expect((Date.parse(body.end_date) - Date.parse(body.start_date)) / 86_400_000).toBe(13);

	const day = new Date(Date.parse(body.start_date) + 2 * 86_400_000).toISOString().slice(0, 10);
	const row = table.getByRole('row', { name: new RegExp(day) });
	await expect(row).toContainText('60 bpm · chest_strap');
	await expect(row).toContainText('61.5 bpm · garmin');
	await expect(row).toContainText('Changed');
	await row.getByText('Why').nth(1).click();
	await expect(row.getByText('Mean of chest_strap and garmin: 61.5 bpm.')).toBeVisible();

	// The changes from the rule in effect are listed field by field.
	await page.getByText('Changes from the rule in effect').click();
	await expect(page.getByRole('row', { name: /strategy\.op/ })).toContainText('"mean_across_sources"');
});

test('save creates a version; history shows the diff and activates another version', async ({ page, rules }) => {
	await page.goto('/rules/new?metric=heart_rate');
	await page.getByRole('button', { name: /Strategy/ }).click();
	await page.getByRole('radio', { name: /^Take the highest/ }).check();
	await page.getByRole('button', { name: /Review/ }).click();
	await page.getByLabel('Note (optional)').fill('try the max');
	await page.getByRole('button', { name: 'Save version' }).click();

	await expect(page).toHaveURL('/rules/heart_rate?saved=2');
	expect(rules.posted[0].strategy).toEqual({ op: 'maximum_across_sources' });
	await expect(page.getByText('Saved version 2.')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'In effect: version 2' })).toBeVisible();
	const history = page.getByRole('region', { name: 'Version history' });
	const v1 = history.getByRole('row', { name: /rule:heart_rate:1/ });
	await expect(v1).toContainText('from builtin:heart_rate:1');
	await expect(history.getByRole('row', { name: /rule:heart_rate:2/ })).toContainText('try the max');
	await expect(page.getByRole('table', { name: 'Changes from version 2 to version 1' }).getByRole('row', { name: /strategy\.op/ })).toContainText(
		'"maximum_across_sources"'
	);

	await v1.getByRole('button', { name: 'Activate version 1' }).click();
	await expect(page.getByText('Version 1 is now active.')).toBeVisible();
	await expect(page.getByRole('heading', { name: 'In effect: version 1' })).toBeVisible();
	await expect(history.getByRole('row', { name: /rule:heart_rate:1/ })).toContainText('Active');
	await expect(history.getByRole('button', { name: 'Activate version 2' })).toBeVisible();
});

test('server field errors are shown by the inputs of their step', async ({ page }) => {
	await page.goto('/rules/new?metric=resting_heart_rate');
	await page.getByRole('group', { name: 'Group 1', exact: true }).getByLabel('Group id').fill('Bad Id');
	await page.getByRole('button', { name: /Review/ }).click();
	await page.getByRole('button', { name: 'Save version' }).click();

	await expect(page.getByRole('heading', { name: '2. Sources' })).toBeFocused();
	const id = page.getByRole('group', { name: 'Group 1', exact: true }).getByLabel('Group id');
	await expect(id).toHaveAttribute('aria-invalid', 'true');
	await expect(page.getByText('must start with a lowercase letter and use only a-z, 0-9 and _')).toBeVisible();
	await expect(page.getByRole('alert')).toContainText('invalid rule');
});
