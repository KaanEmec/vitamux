// The six data states of a resolved value: a shape, a colour token (--status-*) and a word,
// so status never depends on colour alone. Shapes are on a 12×12 grid; `ring` adds an outline.
export type DataStatus = 'direct' | 'fallback' | 'calculated' | 'overridden' | 'partial' | 'no_data';

const circle = 'M6 1.5a4.5 4.5 0 1 0 0 9 4.5 4.5 0 0 0 0-9z';

export const dataStatus: Record<DataStatus, { label: string; meaning: string; shape: string; ring?: boolean }> = {
	direct: { label: 'Direct', meaning: 'From the rule’s first-choice source', shape: circle },
	fallback: { label: 'Fallback', meaning: 'A lower-priority source filled this window', shape: 'M6 1 11 6 6 11 1 6z' },
	calculated: { label: 'Calculated', meaning: 'Combined from several sources', shape: 'M6 1.5 10.8 10.5H1.2z' },
	overridden: { label: 'Overridden', meaning: 'You set, forced or excluded an input', shape: 'M2 2h8v8H2z' },
	partial: { label: 'Partial', meaning: 'Window still open or below coverage', shape: 'M6 1.5a4.5 4.5 0 0 0 0 9z', ring: true },
	no_data: { label: 'No data', meaning: 'Nothing stored for this window', shape: '', ring: true }
};

export const ringPath = circle;

/** A resolved value's display status: `partial` wins over a direct or calculated result. */
export function displayStatus(status: string, partial = false): DataStatus {
	if (partial && status !== 'no_data' && status !== 'overridden') return 'partial';
	return status in dataStatus ? (status as DataStatus) : 'no_data';
}
