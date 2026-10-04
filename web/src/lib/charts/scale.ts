// Scales, ticks, paths and formatting for the chart kit. Plain functions, no dependencies.
// Time is epoch milliseconds; local clock times use the person's IANA timezone when given.
export type Domain = [number, number];

const MIN = 60_000;
const HOUR = 60 * MIN;
export const DAY = 24 * HOUR;

/** Linear map from a domain to a pixel range. */
export function linear([d0, d1]: Domain, [r0, r1]: Domain): (v: number) => number {
	const k = d1 === d0 ? 0 : (r1 - r0) / (d1 - d0);
	return (v) => r0 + (v - d0) * k;
}

/** [min, max] of the finite values, padded by `pad` of the span on both sides; `empty` when there are none. */
export function extent(values: Iterable<number | null | undefined>, pad = 0, empty: Domain = [0, 1]): Domain {
	let lo = Infinity;
	let hi = -Infinity;
	for (const v of values) {
		if (v == null || !Number.isFinite(v)) continue;
		if (v < lo) lo = v;
		if (v > hi) hi = v;
	}
	if (lo > hi) return empty;
	const p = (hi - lo || Math.abs(hi) || 1) * pad;
	return [lo - p, hi + p];
}

/** About `count` round values covering the domain. */
export function ticks([lo, hi]: Domain, count: number): number[] {
	if (!(hi > lo)) return [lo];
	const raw = (hi - lo) / Math.max(count, 1);
	const mag = 10 ** Math.floor(Math.log10(raw));
	const step = [1, 2, 5, 10].map((m) => m * mag).find((s) => s >= raw) ?? 10 * mag;
	const out: number[] = [];
	for (let v = Math.ceil(lo / step) * step; v <= hi + step * 1e-9; v += step) out.push(+v.toFixed(10));
	return out;
}

const timeSteps = [MIN, 5 * MIN, 15 * MIN, 30 * MIN, HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY, 14 * DAY];
const monthSteps = [1, 3, 6, 12];

/** About `count` tick instants on local clock boundaries (hours, midnights, month starts). */
export function timeTicks([lo, hi]: Domain, count: number, timeZone?: string): { ticks: number[]; format: (t: number) => string } {
	const span = (hi - lo) / Math.max(count, 1);
	const step = timeSteps.find((s) => s >= span);
	const fmt = (o: Intl.DateTimeFormatOptions) => new Intl.DateTimeFormat(undefined, { timeZone, ...o });
	const out: number[] = [];
	if (step) {
		const off = offset(lo, timeZone);
		for (let t = Math.ceil((lo + off) / step) * step - off; t <= hi; t += step) out.push(t);
		const f = step < DAY ? fmt({ hour: '2-digit', minute: '2-digit' }) : fmt({ day: 'numeric', month: 'short' });
		return { ticks: out, format: (t) => f.format(t) };
	}
	const months = monthSteps.find((m) => m * 30 * DAY >= span) ?? Math.ceil(span / (365 * DAY)) * 12;
	const p = parts(lo, timeZone);
	for (let m = Math.ceil((p.month - 1) / months) * months, i = 0; i < 400; m += months, i++) {
		const wall = Date.UTC(p.year, m, 1);
		const t = wall - offset(wall, timeZone);
		if (t > hi) break;
		if (t >= lo) out.push(t);
	}
	const f = months >= 12 ? fmt({ year: 'numeric' }) : fmt({ month: 'short', year: '2-digit' });
	return { ticks: out, format: (t) => f.format(t) };
}

function parts(t: number, timeZone?: string) {
	const p: Record<string, number> = {};
	const f = new Intl.DateTimeFormat('en-US', { timeZone, hourCycle: 'h23', year: 'numeric', month: 'numeric', day: 'numeric', hour: 'numeric', minute: 'numeric' });
	for (const x of f.formatToParts(t)) if (x.type !== 'literal') p[x.type] = Number(x.value);
	return p as { year: number; month: number; day: number; hour: number; minute: number };
}

/** UTC offset in ms of `timeZone` (or the browser's) at instant `t`: local = t + offset. */
function offset(t: number, timeZone?: string): number {
	const p = parts(t, timeZone);
	return Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute) - Math.floor(t / MIN) * MIN;
}

/** "Sun 4 Oct, 07:12" (or without the time) in the timezone. */
export function formatInstant(t: number, timeZone?: string, withTime = true): string {
	const o: Intl.DateTimeFormatOptions = { weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', timeZone };
	if (withTime) Object.assign(o, { hour: '2-digit', minute: '2-digit' });
	return new Intl.DateTimeFormat(undefined, o).format(t);
}

const numberFormats = new Map<number, Intl.NumberFormat>();
/** A number with at most `digits` decimals, in the browser's locale. */
export function formatNumber(v: number, digits = 1): string {
	let f = numberFormats.get(digits);
	if (!f) numberFormats.set(digits, (f = new Intl.NumberFormat(undefined, { maximumFractionDigits: digits })));
	return f.format(v);
}

/**
 * SVG path through the points; a null y breaks the line. Dense series (a 14,400-point day) are
 * reduced to their min and max per half-pixel column, which keeps the shape at any density.
 * `step` draws a step-after line (each reading holds until the next).
 */
export function linePath(xs: number[], ys: (number | null)[], sx: (v: number) => number, sy: (v: number) => number, step = false): string {
	let d = '';
	let pen = false;
	let col = NaN;
	let lo = 0;
	let hi = 0;
	let n = 0;
	const flush = () => {
		if (!n) return;
		const a = sy(lo).toFixed(1);
		const b = sy(hi).toFixed(1);
		d += `${pen ? 'L' : 'M'}${col} ${a}${n > 1 && a !== b ? `L${col} ${b}` : ''}`;
		pen = true;
		n = 0;
	};
	for (let i = 0; i < xs.length; i++) {
		const y = ys[i];
		if (y == null || !Number.isFinite(y)) {
			flush();
			pen = false;
			col = NaN;
			continue;
		}
		const x = Math.round(sx(xs[i]) * 2) / 2;
		if (step) {
			d += pen ? `H${x}V${sy(y).toFixed(1)}` : `M${x} ${sy(y).toFixed(1)}`;
			pen = true;
		} else if (x !== col) {
			flush();
			col = x;
			lo = hi = y;
			n = 1;
		} else {
			if (y < lo) lo = y;
			if (y > hi) hi = y;
			n++;
		}
	}
	flush();
	return d;
}

/** Closed area between `lo` and `hi` (a range band); windows missing either bound are skipped. */
export function bandPath(xs: number[], lo: (number | null)[], hi: (number | null)[], sx: (v: number) => number, sy: (v: number) => number): string {
	const top: string[] = [];
	const bottom: string[] = [];
	xs.forEach((x, i) => {
		const a = lo[i];
		const b = hi[i];
		if (a == null || b == null) return;
		top.push(`${sx(x).toFixed(1)} ${sy(b).toFixed(1)}`);
		bottom.unshift(`${sx(x).toFixed(1)} ${sy(a).toFixed(1)}`);
	});
	return top.length ? `M${top.join('L')}L${bottom.join('L')}Z` : '';
}

/** Index of the value in sorted `xs` nearest to `x`, or -1 when empty. */
export function nearest(xs: number[], x: number): number {
	if (!xs.length) return -1;
	let lo = 0;
	let hi = xs.length - 1;
	while (hi - lo > 1) {
		const m = (lo + hi) >> 1;
		if (xs[m] < x) lo = m;
		else hi = m;
	}
	return Math.abs(xs[hi] - x) < Math.abs(xs[lo] - x) ? hi : lo;
}

/** Records the "vx-chart-render" User Timing measure from `start` to the next paint. */
export function measureRender(start: number) {
	requestAnimationFrame(() => performance.measure('vx-chart-render', { start }));
}
