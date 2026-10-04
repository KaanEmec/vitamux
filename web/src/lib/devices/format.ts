// Display helpers for the device screens.

/** "HKQuantityTypeIdentifierHeartRate" as "Heart rate"; anything else unchanged. */
export function typeLabel(id: string): string {
	const name = id.replace(/^HK(?:Quantity|Category|Correlation|Data|Characteristic)?TypeIdentifier/, '');
	if (name === id || !name) return id;
	const words = name.replace(/([a-z0-9])([A-Z])/g, '$1 $2').toLowerCase();
	return words.charAt(0).toUpperCase() + words.slice(1);
}

/** m:ss for a number of milliseconds, never negative. */
export function countdown(ms: number): string {
	const s = Math.max(0, Math.ceil(ms / 1000));
	return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}
