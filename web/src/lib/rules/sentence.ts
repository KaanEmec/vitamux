// A rule in plain words (frontend.md#rule-builder): what the rule lens, the catalogue and the
// builder show instead of the JSON. Neutral and descriptive: it says what the rule does, never
// whether a source or a value is better.
import { ops, type Op, type Rule } from './rule.ts';

/** The rule lens's strategy pills. */
export const opShort: Record<Op, string> = {
	first_available: 'First available',
	single_source: 'One source',
	mean_across_sources: 'Mean',
	minimum_across_sources: 'Lowest',
	maximum_across_sources: 'Highest',
	sum_across_sources: 'Sum',
	latest: 'Latest',
	earliest: 'Earliest',
	event_priority: 'Whole event'
};

const nouns: Record<string, string> = {
	hour: 'hour',
	local_day: 'day',
	local_night: 'night',
	sleep_episode: 'sleep episode',
	reading: 'reading',
	latest: 'latest value'
};

/** What one window is called: "night", "5-minute bucket". */
function windowNoun(w: Rule['window']): string {
	if (w.kind === 'bucket') return `${parseInt(w.size ?? '', 10) || '?'}-minute bucket`;
	return nouns[w.kind] ?? w.kind;
}

/** "For each night, use the first source in order with data …". */
export function ruleSentence(r: Rule): string {
	const noun = windowNoun(r.window);
	const lead = r.window.kind === 'latest' ? 'For the latest value' : `For each ${noun}`;
	const cov = r.quality?.min_coverage;
	const covered = typeof cov === 'number' && cov > 0 ? ` with at least ${Math.round(cov * 100)}% coverage` : '';
	const min = r.strategy.min_sources;
	const atLeast = ops.find((o) => o.op === r.strategy.op)?.pooling && typeof min === 'number' && min > 1 ? `, when at least ${min} have data` : '';
	const none = `; if none has, the ${noun} has no value`;

	const body: Record<Op, string> = {
		single_source: `use only ${r.groups[0]?.id ?? 'one source'}${covered}; without it, the ${noun} has no value`,
		first_available: `use the first source in order ${covered ? covered.trim() : 'with data'}${none}`,
		mean_across_sources: `average the sources${covered}${atLeast}`,
		minimum_across_sources: `take the lowest of the sources${covered}${atLeast}`,
		maximum_across_sources: `take the highest of the sources${covered}${atLeast}`,
		sum_across_sources: `add the sources together${covered}${atLeast}; the same activity can be counted twice`,
		latest: `use the newest value of the sources${covered}; ties go by order`,
		earliest: `use the oldest value of the sources${covered}; ties go by order`,
		event_priority: `use the whole event from the first source in order that recorded one${covered}`
	};
	const out = [`${lead}, ${body[r.strategy.op] ?? r.strategy.op}.`];
	if (r.compose) out.push('Each hour is picked first, then the hours are added up.');
	if (r.follow) out.push(`It uses the source that ${r.follow} selected when it can.`);
	const ex = r.exclude?.length ?? 0;
	if (ex) out.push(`${ex} excluded ${ex === 1 ? 'source is' : 'sources are'} never used.`);
	return out.join(' ');
}
