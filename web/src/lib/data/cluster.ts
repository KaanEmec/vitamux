// Groups overlapping workouts for display, with the rule of docs/architecture/resolution.md
// (overlap of at least 0.6 of the shorter workout, compatible sport: equal, or one is
// "other"; a cluster never holds two specific sports). It only arranges source rows; which
// workout a rule selects comes from the resolved endpoints.
import type { Schemas } from '#lib/api/client.ts';

export type Workout = Schemas['Workout'];

export interface Cluster {
	start: number;
	end: number;
	/** The specific sport of the members, or "other" when none has one. */
	sport: string;
	workouts: Workout[];
}

const minOverlap = 0.6;

export function clusterWorkouts(workouts: Workout[]): Cluster[] {
	const sorted = [...workouts].sort((a, b) => Date.parse(a.start_at) - Date.parse(b.start_at) || a.id.localeCompare(b.id));
	const clusters: Cluster[] = [];
	for (const w of sorted) {
		const start = Date.parse(w.start_at);
		const end = Date.parse(w.end_at);
		const home = clusters.find((c) => compatible(c.sport, w.sport) && c.workouts.some((m) => overlaps(m, start, end)));
		if (!home) {
			clusters.push({ start, end, sport: w.sport, workouts: [w] });
			continue;
		}
		home.workouts.push(w);
		home.start = Math.min(home.start, start);
		home.end = Math.max(home.end, end);
		if (home.sport === 'other') home.sport = w.sport;
	}
	return clusters;
}

function compatible(a: string, b: string): boolean {
	return a === b || a === 'other' || b === 'other';
}

function overlaps(m: Workout, start: number, end: number): boolean {
	const ms = Date.parse(m.start_at);
	const me = Date.parse(m.end_at);
	const shorter = Math.min(me - ms, end - start);
	if (shorter <= 0) return false;
	return (Math.min(me, end) - Math.max(ms, start)) / shorter >= minOverlap;
}
