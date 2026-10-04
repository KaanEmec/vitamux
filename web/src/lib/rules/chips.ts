// One-click selectors for the rule builder, from the origins and devices the owner's data came from
// (apple-health.md#origins-and-relays): an app, a device type, and relayed or direct.
import type { Schemas } from '../api/client.ts';
import { providerLabel } from '../connections/connections.ts';
import { brandName, selectorKey, selectorText, type Selector } from './rule.ts';

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
		if (!d.device_type || d.merged_into) continue; // a merged device's records carry its target's type
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
		device_manufacturer: uniq(devices.map((d) => d.manufacturer)),
		device_type: uniq(devices.map((d) => d.device_type)),
		device_model: uniq(devices.map((d) => d.model))
	};
}

const isApple = (manufacturer: string) => /^apple\b/i.test(manufacturer);

/** A device's name as people say it: "Apple Watch", "iPhone", "Garmin Forerunner 965" (no doubled brand). */
export function deviceLabel(manufacturer: string, model: string): string {
	const brand = brandName(manufacturer);
	if (model.toLowerCase().startsWith(brand.toLowerCase())) return model;
	if (isApple(manufacturer) && /^iphone|^ipad|^ipod/i.test(model)) return model;
	return `${brand} ${model}`;
}

/**
 * Named choices for the rule builder's "Choose a source or device", from the owner's own devices
 * and providers (resolution.md#selectors-and-validation): whole sources, a brand, a model, or one
 * device the owner named. Merged devices are left out; their records live on the target.
 */
export function sourceChoices(devices: Schemas['SourceDevice'][], providers: Schemas['Provider'][]): Chip[] {
	const live = devices.filter((d) => !d.merged_into);
	const out = new Map<string, Chip>();
	const add = (label: string, selector: Selector) => out.set(selectorKey(selector), { label, selector });

	const codes = new Set([...live.map((d) => d.provider), ...providers.filter((p) => p.connections > 0).map((p) => p.code)]);
	const name = (code: string) => providers.find((p) => p.code === code)?.name ?? providerLabel(code);
	for (const code of [...codes].sort((a, b) => name(a).localeCompare(name(b)))) add(`${name(code)} (all data)`, { provider: code });

	const brands = [...new Set(live.flatMap((d) => (d.manufacturer ? [d.manufacturer] : [])))].sort();
	for (const m of brands) {
		const models = [...new Set(live.flatMap((d) => (d.manufacturer === m && d.model ? [d.model] : [])))].sort();
		if (isApple(m)) {
			for (const model of models) add(deviceLabel(m, model), { provider: 'apple_health', device_manufacturer: m, device_model: model });
			continue;
		}
		add(`${brandName(m)} (any device)`, { device_manufacturer: m });
		for (const model of models) add(deviceLabel(m, model), { device_manufacturer: m, device_model: model });
	}
	for (const d of live) if (d.name) add(d.name, { device_id: d.id });
	return [...out.values()];
}
