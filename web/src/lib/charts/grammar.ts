// Chart grammar (docs/architecture/frontend.md#chart-grammar): the metric catalogue's
// aggregation and group pick the view, so no metric needs its own chart code.
import type { Schemas } from '../api/client.ts';

export type ChartView = 'line-band' | 'bars' | 'step' | 'line-baseline' | 'sleep' | 'dumbbell';

const byAggregation: Record<Schemas['Metric']['aggregation'], ChartView> = {
	intensive: 'line-band', // TimeSeries with a min–max band
	additive: 'bars', // Bars per window
	latest: 'step', // TimeSeries step, with readings
	daily_summary: 'line-baseline', // TimeSeries with a baseline
	sleep_derived: 'sleep' // Bars stacked by stage, Hypnogram per night
};

/** The view for a catalogue metric (GET /metrics): blood-pressure groups are dumbbells. */
export function chartFor(metric: Pick<Schemas['Metric'], 'aggregation' | 'group'>): ChartView {
	if (metric.group?.startsWith('bp_')) return 'dumbbell';
	return byAggregation[metric.aggregation];
}
