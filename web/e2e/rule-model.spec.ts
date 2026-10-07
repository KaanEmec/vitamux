// The rule draft model against the fixture VitamuxKit also checks (fixtures/rule-model.json,
// J22.10): each case's form gives exactly the spec JSON and the plain sentence the panel gives,
// so a rule saved on the phone equals the same rule saved here. A plain import; no page.
import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';
import { fromSpec, toSpec, type Form, type Rule } from '../src/lib/rules/rule.ts';
import { ruleSentence } from '../src/lib/rules/sentence.ts';

interface Case {
	note: string;
	rule?: Rule;
	form: Omit<Form, 'groups'> & { groups: Omit<Form['groups'][number], 'key'>[] };
	spec: string;
	sentence: string;
}

const fixture: { synthetic: boolean; cases: Case[] } = JSON.parse(
	readFileSync(new URL('../../fixtures/rule-model.json', import.meta.url), 'utf8')
);

const withKeys = (f: Case['form']): Form => ({ ...f, groups: f.groups.map((g, i) => ({ ...g, key: i + 1 })) });
const withoutKeys = (f: Form) => ({ ...f, groups: f.groups.map(({ id, match }) => ({ id, match })) });

test('the fixture is synthetic and reads every strategy', () => {
	expect(fixture.synthetic).toBe(true);
	const ops = new Set(fixture.cases.map((c) => (JSON.parse(c.spec) as Rule).strategy.op));
	expect(ops.size).toBe(9);
});

for (const c of fixture.cases) {
	test(c.note, () => {
		if (c.rule) expect(JSON.parse(JSON.stringify(withoutKeys(fromSpec(c.rule))))).toEqual(c.form);
		const spec = toSpec(withKeys(c.form));
		expect(JSON.stringify(spec)).toBe(c.spec);
		expect(ruleSentence(spec)).toBe(c.sentence);
	});
}
