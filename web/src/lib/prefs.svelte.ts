// Per-browser display preferences (not server settings): the theme override and the
// collapsed sidebar and the Explore source strip. "system" follows prefers-color-scheme; "light" and "dark" set
// <html data-theme> (styles/tokens.css). Storage can be unavailable, so every access is guarded.
export type Theme = 'system' | 'light' | 'dark';

const themes: Theme[] = ['system', 'light', 'dark'];
const stored = read('vx-theme') as Theme;

export const prefs = $state({
	theme: themes.includes(stored) ? stored : 'system',
	sidebarCollapsed: read('vx-sidebar') === 'collapsed',
	sourceStrip: read('vx-source-strip') === 'on'
});
apply(prefs.theme);

export function setTheme(t: Theme) {
	prefs.theme = t;
	apply(t);
	write('vx-theme', t === 'system' ? null : t);
}

export function toggleSidebar() {
	prefs.sidebarCollapsed = !prefs.sidebarCollapsed;
	write('vx-sidebar', prefs.sidebarCollapsed ? 'collapsed' : null);
}

export function toggleSourceStrip() {
	prefs.sourceStrip = !prefs.sourceStrip;
	write('vx-source-strip', prefs.sourceStrip ? 'on' : null);
}

function apply(t: Theme) {
	if (t === 'system') delete document.documentElement.dataset.theme;
	else document.documentElement.dataset.theme = t;
}

function read(k: string): string | null {
	try {
		return localStorage.getItem(k);
	} catch {
		return null;
	}
}

function write(k: string, v: string | null) {
	try {
		if (v === null) localStorage.removeItem(k);
		else localStorage.setItem(k, v);
	} catch {
		// Private mode or blocked storage: the choice lasts for this page only.
	}
}
