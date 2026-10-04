// Display helpers for the Explore inventory (GET /inventory): names, sections, the latest
// value and the filters. Items keep the server's order inside a section.
import type { Schemas } from '../api/client.ts';
import { duration, formatValue, metricLabel, today } from '../data/format.ts';

export type Item = Schemas['InventoryItem'];
type Device = Schemas['DeviceRef'];
type Origin = Schemas['OriginRef'];

const groupNames: Record<string, string> = { bp_reading: 'Blood pressure', body_composition: 'Body composition' };

export function itemName(it: Item): string {
	if (it.kind === 'analyte') return it.analyte?.name ?? it.code;
	if (it.kind === 'group') return groupNames[it.code] ?? metricLabel(it.code);
	return metricLabel(it.code);
}

/** The section an item is listed under: the catalogue section for metrics and groups. */
export function sectionOf(it: Item, metrics: Map<string, Item>): string {
	switch (it.kind) {
		case 'metric':
			return it.metric?.section ?? 'Other';
		case 'group':
			return it.components?.map((c) => metrics.get(c)?.metric?.section).find(Boolean) ?? groupNames[it.code] ?? 'Other';
		case 'sleep':
			return 'Sleep';
		case 'workouts':
			return 'Activity';
		case 'event':
			return 'Events';
		default:
			return 'Lab analytes';
	}
}

/** The newest record as printed: value and unit, a group's components, a level or text. */
export function latestValue(it: Item): { value: string; unit: string } {
	const l = it.latest;
	if (!l) return { value: '–', unit: '' };
	if (it.kind === 'sleep' && l.value != null) return { value: duration(l.value), unit: 'asleep' };
	if (l.components) return { value: formatValue(l.components), unit: '' };
	if (l.value != null) return { value: formatValue(l.value), unit: l.unit ?? '' };
	return { value: l.text ?? l.level ?? '–', unit: '' };
}

/** The last record's time today, else its date. */
export function lastSeen(it: Item): string {
	if (!it.last_at) return it.last_date || '–';
	const t = new Date(it.last_at);
	if (it.last_date === today()) return t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
	const year = t.getFullYear() === new Date().getFullYear() ? undefined : 'numeric';
	return t.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year });
}

export const deviceKey = (d: Device) => d.id ?? d.type ?? '';
export const deviceLabel = (d: Device) => d.model ?? d.type ?? d.id ?? 'device';
export const originLabel = (o: Origin) => o.name ?? o.key;

export interface Filter {
	text: string;
	provider: string;
	device: string;
	origin: string;
}

/** Whether the item matches every filter; the text matches its name, code, devices, origins or analyte. */
export function matches(it: Item, f: Filter): boolean {
	if (f.provider && !it.providers.includes(f.provider)) return false;
	if (f.device && !it.devices.some((d) => deviceKey(d) === f.device)) return false;
	if (f.origin && !it.origins.some((o) => o.key === f.origin)) return false;
	const q = f.text.trim().toLowerCase();
	if (!q) return true;
	const hay = [itemName(it), it.code, it.analyte?.code, ...it.devices.map(deviceLabel), ...it.origins.map(originLabel)];
	return hay.some((s) => s?.toLowerCase().includes(q));
}
