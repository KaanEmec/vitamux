// Metric hue and icon (docs/architecture/frontend.md#design-system): the catalogue section of a
// metric (GET /metrics `section`) picks its hue and icon tile; a few codes refine it (HRV and
// energy inside their sections, derived codes, and the dashboard's family cards). The hue names
// the metric only; it never says whether a value is good.
import type { Schemas } from '../api/client.ts';
import type { icons } from './icons.ts';

type MetricHue = 'activity' | 'energy' | 'heart' | 'hrv' | 'sleep' | 'body' | 'bp' | 'respiratory' | 'other';

export interface MetricLook {
	/** `var(--metric-<hue>)`: strokes, fills, the tile icon. */
	color: string;
	/** `var(--metric-<hue>-tint)`: the icon tile ground. */
	tint: string;
	icon: keyof typeof icons;
}

const bySection: Record<string, MetricHue> = {
	Activity: 'activity',
	Mobility: 'activity',
	'Heart and circulation': 'heart',
	'Blood pressure': 'bp',
	'Respiration and oxygen': 'respiratory',
	Temperature: 'body',
	'Body composition': 'body',
	'Glucose and metabolism': 'energy',
	'Nutrition and intake': 'energy',
	Sleep: 'sleep'
};

// Codes whose hue is not their section's: family cards and groups, and the Derived section.
const byCode: Record<string, MetricHue> = {
	sleep: 'sleep',
	blood_pressure: 'bp',
	bp_reading: 'bp',
	body_composition: 'body',
	resting_heart_rate_nocturnal: 'heart',
	spo2_night_min: 'respiratory'
};

const iconOf: Record<MetricHue, keyof typeof icons> = {
	activity: 'footprints',
	energy: 'flame',
	heart: 'heart',
	hrv: 'activity',
	sleep: 'moon',
	body: 'scale',
	bp: 'droplet',
	respiratory: 'wind',
	other: 'metric'
};

/** The hue of a metric, card or group code, given its catalogue section when known. */
function metricHue(code: string, section?: Schemas['Metric']['section']): MetricHue {
	if (code.startsWith('hrv_')) return 'hrv';
	if (code.endsWith('_energy')) return 'energy';
	return byCode[code] ?? (section && bySection[section]) ?? 'other';
}

export function metricLook(code: string, section?: string): MetricLook {
	const hue = metricHue(code, section);
	return { color: `var(--metric-${hue})`, tint: `var(--metric-${hue}-tint)`, icon: iconOf[hue] };
}
