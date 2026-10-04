// One-click selectors for the rule builder, from the origins and devices the owner's data came from
// (apple-health.md#origins-and-relays): an app, a device type, and relayed or direct.
import type { Schemas } from '../api/client.ts';
import { selectorText, type Selector } from './rule.ts';

export interface Chip {
	label: string;
	selector: Selector;
}

/** Transport providers (those with origins) get relayed/direct chips, and their device types the direct ones. */
export function selectorChips(origins: Schemas['DataOrigin'][], devices: Schemas['SourceDevice'][]): Chip[] {
	const chips = new Map<string, Selector>();
	const add = (s: Selector) => chips.set(selectorText(s), s);
	const transports = new Set(origins.map((o) => o.provider));
	for (const p of transports) {
		add({ provider: p, relayed: false });
		add({ provider: p, relayed: true });
	}
	for (const d of devices) {
		if (!d.device_type) continue;
		add(transports.has(d.provider) ? { provider: d.provider, device_type: d.device_type, relayed: false } : { provider: d.provider, device_type: d.device_type });
	}
	for (const o of origins) add({ provider: o.provider, origin_key: o.origin_key });
	return [...chips].map(([label, selector]) => ({ label, selector }));
}

/** Values for the selector inputs' suggestions. */
export function seenValues(origins: Schemas['DataOrigin'][], devices: Schemas['SourceDevice'][]) {
	const uniq = (xs: (string | null | undefined)[]) => [...new Set(xs.filter((x): x is string => !!x))];
	return {
		origin_key: uniq(origins.map((o) => o.origin_key)),
		origin_name: uniq(origins.map((o) => o.name)),
		device_type: uniq(devices.map((d) => d.device_type)),
		device_model: uniq(devices.map((d) => d.model))
	};
}
