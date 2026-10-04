// Where each Explore item opens (docs/plan/E21-visualisation/J21.8-explore.md). Metrics get the
// generic detail page; sleep, workouts, events, groups and lab analytes their specialised views.
import type { Schemas } from '../api/client.ts';

type Kind = Schemas['InventoryItem']['kind'];

const groups: Record<string, string> = {
	bp_reading: '/explore/blood-pressure',
	body_composition: '/explore/body-composition'
};

// Rule families a dashboard card or the command palette may name instead of a catalogue code.
const families: Record<string, string> = {
	sleep: '/explore/sleep',
	blood_pressure: '/explore/blood-pressure'
};

export function exploreHref({ kind, code }: { kind: Kind; code: string }): string {
	const c = encodeURIComponent(code);
	switch (kind) {
		case 'sleep':
			return '/explore/sleep';
		case 'workouts':
			return '/explore/workouts';
		case 'event':
			return `/explore/events?code=${c}`;
		case 'analyte':
			return `/lab/analytes/${c}`;
		case 'group':
			return groups[code] ?? `/explore/${c}`;
		default:
			return families[code] ?? `/explore/${c}`;
	}
}

/** The dashboard card key of an item (a catalogue code or rule family), or null when it has no card. */
export function pinKey({ kind, code }: { kind: Kind; code: string }): string | null {
	if (kind === 'metric') return code;
	if (kind === 'sleep') return 'sleep';
	if (kind === 'group' && code === 'bp_reading') return 'blood_pressure';
	return null;
}
