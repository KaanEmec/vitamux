// The chart grammar against the fixture VitamuxKit also checks (fixtures/chart-grammar.json,
// J22.6), so the panel and the iOS app cannot drift. A plain import of grammar.ts; no page.
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import type { Schemas } from '../src/lib/api/client.ts';
import { chartFor, type ChartView } from '../src/lib/charts/grammar.ts';

interface Case {
	note: string;
	metric: Schemas['Metric'];
	expect: { view: ChartView; day: Schemas['Intraday'] | null };
}

const fixture: { synthetic: boolean; cases: Case[] } = JSON.parse(
	readFileSync(new URL('../../fixtures/chart-grammar.json', import.meta.url), 'utf8')
);

test('the fixture is synthetic and covers every view', () => {
	expect(fixture.synthetic).toBe(true);
	const views = new Set(fixture.cases.map((c) => c.expect.view));
	expect([...views].sort()).toEqual(['bars', 'dumbbell', 'line-band', 'line-baseline', 'sleep', 'step']);
	expect(fixture.cases.some((c) => c.expect.day === null && c.metric.aggregation === 'intensive')).toBe(true);
});

for (const c of fixture.cases) {
	test(`${c.metric.code}: ${c.note}`, () => {
		expect(chartFor(c.metric)).toBe(c.expect.view);
		expect(c.metric.intraday ?? null).toEqual(c.expect.day);
	});
}
