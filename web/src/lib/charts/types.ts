// Shapes shared by the chart kit.
import type { DataStatus } from '../ui/status.ts';

/** Tooltip and keyboard-announcement content for one point. */
export interface Tip {
	title: string;
	/** The point's own value, shown large: value, unit, status and the providers behind it. */
	lead?: { value: string; unit?: string; status?: DataStatus; providers?: string[] };
	rows: { label: string; value: string; source?: string; status?: DataStatus }[];
	note?: string;
}

/** An action on a point, offered in the tooltip once it is pinned (Explain, Override, Raw records). */
export interface TipAction {
	label: string;
	run: (i: number) => void;
}

/** The accessible table behind a chart (newest first). */
export interface TableData {
	columns: string[];
	rows: string[][];
}

/** One line on a TimeSeries. */
export interface Series {
	label: string;
	/** Epoch ms, ascending. */
	xs: number[];
	ys: (number | null)[];
	/** Provider code: picks the stable source colour. Without it the series uses the metric hue. */
	source?: string;
	/** line (default), ghost (a draft overlay, dashed) or dots (readings without a line, e.g. lab results). */
	style?: 'line' | 'ghost' | 'dots';
	/** Per point; non-direct statuses get a marker (shape + colour). */
	status?: (DataStatus | null)[];
	/** Per point: the providers behind the value (GET /resolved/series `providers`), for the tooltip. */
	providers?: (string[] | null)[];
}

const statusWord = (s?: DataStatus) => (s ? ` (${s.replace('_', ' ')})` : '');

export function tipText(t: Tip): string {
	const lead = t.lead && `${t.lead.value}${t.lead.unit ? ` ${t.lead.unit}` : ''}${statusWord(t.lead.status)}${t.lead.providers?.length ? ` from ${t.lead.providers.join(', ')}` : ''}`;
	const rows = t.rows.map((r) => `${r.label} ${r.value}${statusWord(r.status)}`);
	return `${t.title}: ${[lead, ...rows].filter(Boolean).join(', ')}${t.note ? `. ${t.note}` : ''}`;
}
