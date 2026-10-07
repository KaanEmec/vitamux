// An ECG waveform laid out at the standard paper scale, 25 mm/s and 10 mm/mV (one millimetre is
// `pxPerMM` CSS pixels), the same layout as the app's strip. The trace is reduced once to the
// lowest and highest value of each pixel column (kept in time order), so the 15,000 raw voltages
// never reach the DOM. Values are drawn as recorded; nothing is marked or coloured by result.
import type { Schemas } from '../api/client.ts';
import { formatNumber } from '../charts/scale.ts';

export const pxPerMM = 4;
const mmPerSecond = 25;
const mmPerMillivolt = 10;
export const pxPerSecond = pxPerMM * mmPerSecond;
export const pxPerMillivolt = pxPerMM * mmPerMillivolt;

export interface Strip {
	/** SVG path of the trace, x in px from the start, y in px from the top. */
	path: string;
	width: number;
	height: number;
	/** The millivolt bounds of the paper, on whole half-millivolts. */
	low: number;
	high: number;
	duration: number;
	count: number;
	frequency: number | null;
	/** Per second: the lowest and highest value in mV, for the table fallback. */
	seconds: { second: number; low: number; high: number }[];
	/** "30 s at 512 Hz, from −0.3 to 1.0 mV". */
	summary: string;
}

/** Null when the values have no time base (no sampling frequency and no offsets) or none at all. */
export function layoutStrip(doc: Schemas['WaveformDocument']): Strip | null {
	const values = doc.values;
	const offsets = doc.offsets_s?.length === values.length ? doc.offsets_s : null;
	const hz = doc.sampling_frequency_hz && doc.sampling_frequency_hz > 0 ? doc.sampling_frequency_hz : null;
	if (!values.length || (!offsets && !hz)) return null;
	const time = (i: number) => offsets?.[i] ?? i / (hz as number);
	// µV to mV, as recorded; the paper spans at least −0.5 to 1 mV and at most ±5 mV.
	const scale = doc.unit === 'mV' ? 1 : 0.001;
	let lowest = Infinity;
	let highest = -Infinity;
	for (const v of values) {
		if (!Number.isFinite(v)) continue;
		lowest = Math.min(lowest, v * scale);
		highest = Math.max(highest, v * scale);
	}
	if (!Number.isFinite(lowest)) return null;
	const low = Math.max(Math.floor(Math.min(lowest, -0.5) * 2) / 2 - 0.5, -5);
	const high = Math.min(Math.ceil(Math.max(highest, 1) * 2) / 2 + 0.5, 5);
	const duration = time(values.length - 1) + (hz ? 1 / hz : 0);
	const y = (mV: number) => (high - Math.min(Math.max(mV, low), high)) * pxPerMillivolt;

	let d = '';
	let column = NaN;
	let minY = Infinity;
	let maxY = -Infinity;
	let minAt = 0;
	let maxAt = 0;
	const seconds: Strip['seconds'] = [];
	let second = 0;
	let sLow = Infinity;
	let sHigh = -Infinity;
	const flushColumn = () => {
		if (Number.isNaN(column)) return;
		const [a, b] = minAt <= maxAt ? [minY, maxY] : [maxY, minY];
		d += `${d ? 'L' : 'M'}${column},${a.toFixed(1)}`;
		if (b !== a) d += `L${column},${b.toFixed(1)}`;
		minY = Infinity;
		maxY = -Infinity;
	};
	const flushSecond = () => {
		if (Number.isFinite(sLow)) seconds.push({ second, low: sLow, high: sHigh });
		sLow = Infinity;
		sHigh = -Infinity;
	};
	for (let i = 0; i < values.length; i++) {
		const v = values[i];
		if (!Number.isFinite(v)) continue;
		const t = time(i);
		const mV = v * scale;
		while (Math.floor(t) > second) {
			flushSecond();
			second++;
		}
		sLow = Math.min(sLow, mV);
		sHigh = Math.max(sHigh, mV);
		const x = Math.floor(t * pxPerSecond);
		if (x !== column) {
			flushColumn();
			column = x;
		}
		const py = y(mV);
		if (py < minY) [minY, minAt] = [py, i];
		if (py > maxY) [maxY, maxAt] = [py, i];
	}
	flushColumn();
	flushSecond();
	const range = `from ${formatNumber(Math.min(...seconds.map((s) => s.low)), 2)} to ${formatNumber(Math.max(...seconds.map((s) => s.high)), 2)} mV`;
	return {
		path: d,
		width: Math.max(Math.ceil(duration * pxPerSecond), 1),
		height: (high - low) * pxPerMillivolt,
		low,
		high,
		duration,
		count: values.length,
		frequency: hz,
		seconds,
		summary: `${Math.round(duration)} s${hz ? ` at ${formatNumber(hz)} Hz` : ''}, ${range}`
	};
}
