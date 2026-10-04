// Sleep stages: display order (awake on top), labels and the colour token of each.
export const stageOrder = ['awake', 'rem', 'light', 'deep', 'asleep_unspecified', 'in_bed', 'restless', 'out_of_bed', 'unknown'] as const;

export const stageLabels: Record<string, string> = {
	awake: 'Awake',
	rem: 'REM',
	light: 'Light',
	deep: 'Deep',
	asleep_unspecified: 'Asleep',
	in_bed: 'In bed',
	restless: 'Restless',
	out_of_bed: 'Out of bed',
	unknown: 'Unknown'
};

export type StageColor = 'awake' | 'rem' | 'light' | 'deep' | 'other';

export const stageColor = (stage: string): StageColor =>
	stage === 'awake' || stage === 'rem' || stage === 'light' || stage === 'deep' ? stage : 'other';
