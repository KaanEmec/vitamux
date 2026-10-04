// Stable source colours: a provider code maps to a .src-* class (base.css), which sets --src
// from the --src-* tokens. Unknown providers get one of three extra colours by a stable hash,
// so a source keeps its colour across pages and reloads.
const known = new Set(['apple_health', 'whoop', 'withings', 'garmin', 'manual']);

export function sourceClass(provider: string): string {
	if (known.has(provider)) return `src-${provider}`;
	if (provider === 'push' || provider === 'file_import') return 'src-manual';
	let h = 0;
	for (const c of provider) h = (h * 31 + c.charCodeAt(0)) >>> 0;
	return `src-other-${(h % 3) + 1}`;
}
