// Shapes shared by the chart kit.
import type { DataStatus } from '../ui/status.ts';

/** Tooltip and keyboard-announcement content for one point. */
export interface Tip {
	title: string;
	rows: { label: string; value: string; source?: string; status?: DataStatus }[];
	note?: string;
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
	/** Provider code: picks the stable source colour. Without it the series uses the accent. */
	source?: string;
	/** line (default), ghost (a draft overlay, dashed) or dots (readings without a line, e.g. lab results). */
	style?: 'line' | 'ghost' | 'dots';
	/** Per point; non-direct statuses get a marker (shape + colour). */
	status?: (DataStatus | null)[];
}

export const tipText = (t: Tip) =>
	`${t.title}: ${t.rows.map((r) => `${r.label} ${r.value}${r.status ? ` (${r.status.replace('_', ' ')})` : ''}`).join(', ')}${t.note ? `. ${t.note}` : ''}`;
