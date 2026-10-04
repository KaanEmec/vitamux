// The dashboard layout (GET/PUT /settings/dashboard): ordered cards of metric, size and hidden.
// Pure helpers for edit mode; the page owns the state.
import type { Schemas } from '../api/client.ts';
import { metricLabel } from '../data/format.ts';

export type Card = Schemas['DashboardCard'];
export type Size = Card['size'];
export type Catalogue = Schemas['Metric'];

export const sizes: Size[] = ['S', 'M', 'L'];

/** The curated default, as the server serves it until a layout is saved (internal/api/dashboard.go). */
export const defaultCards: Card[] = (
	[
		['sleep', 'L'],
		['resting_heart_rate', 'M'],
		['hrv_rmssd_nightly', 'M'],
		['hrv_sdnn', 'M'],
		['steps', 'M'],
		['vo2max', 'S'],
		['weight', 'S'],
		['blood_pressure', 'S'],
		['spo2', 'S'],
		['respiratory_rate', 'S'],
		['active_energy', 'S'],
		['total_energy', 'S']
	] as [string, Size][]
).map(([metric, size]) => ({ metric, size, hidden: false }));

/** The hero stat tiles the server serves until the owner picks others (at most `heroMax`). */
export const defaultHero = ['steps', 'resting_heart_rate', 'hrv_rmssd_nightly', 'weight'];
export const heroMax = 4;

const labels: Record<string, string> = {
	sleep: 'Sleep',
	blood_pressure: 'Blood pressure',
	hrv_rmssd_nightly: 'HRV · nightly RMSSD',
	hrv_sdnn: 'HRV · SDNN',
	vo2max: 'VO₂ max',
	spo2: 'SpO₂',
	total_energy: 'Total energy'
};

/** A card's title: the curated name, else the code made readable. */
export const cardLabel = (code: string) => labels[code] ?? metricLabel(code);

/** The card a catalogue code is pinned as: family codes (sleep stages, blood pressure) share one. */
export function cardOf(m: Catalogue): string {
	if (m.aggregation === 'sleep_derived') return 'sleep';
	return m.group === 'bp_reading' ? 'blood_pressure' : m.code;
}

/** The card an inventory item belongs on, or '' for events, workouts, lab analytes and other groups. */
export function cardOfItem(it: Schemas['InventoryItem']): string {
	if (it.kind === 'sleep') return 'sleep';
	if (it.kind === 'group') return it.code === 'bp_reading' ? 'blood_pressure' : '';
	return it.kind === 'metric' && it.metric ? cardOf(it.metric) : '';
}

/** Puts the card where `target` is (drag and drop). */
export function moveTo(cards: Card[], metric: string, target: string): Card[] {
	const card = cards.find((c) => c.metric === metric);
	const at = cards.findIndex((c) => c.metric === target);
	if (!card || at < 0 || metric === target) return cards;
	const rest = cards.filter((c) => c.metric !== metric);
	rest.splice(at, 0, card);
	return rest;
}

/** Moves the card one place towards `dir`, past hidden cards (they are not on screen). */
export function move(cards: Card[], metric: string, dir: -1 | 1): Card[] {
	let to = cards.findIndex((c) => c.metric === metric) + dir;
	while (cards[to]?.hidden) to += dir;
	return cards[to] ? moveTo(cards, metric, cards[to].metric) : cards;
}

export const patch = (cards: Card[], metric: string, change: Partial<Card>): Card[] =>
	cards.map((c) => (c.metric === metric ? { ...c, ...change } : c));
