// Stroke icon paths (24×24, drawn by ui/Icon.svelte) used only by the dashboard.
export const dashIcons = {
	edit: 'M4 20h4L19 9l-4-4L4 16z M13.5 6.5l4 4',
	grip: 'M9 6h.01 M15 6h.01 M9 12h.01 M15 12h.01 M9 18h.01 M15 18h.01',
	up: 'M6 14l6-6 6 6',
	down: 'M6 10l6 6 6-6',
	left: 'M15 6l-6 6 6 6',
	right: 'M9 6l6 6-6 6',
	hide: 'M3 3l18 18 M10.6 6.1A9.8 9.8 0 0 1 12 6c6 0 9.5 6 9.5 6a16 16 0 0 1-2.8 3.4 M6.4 7.6A15.6 15.6 0 0 0 2.5 12S6 18 12 18a9 9 0 0 0 4.2-1 M9.9 10a3 3 0 0 0 4.2 4.1',
	plus: 'M12 5v14 M5 12h14'
} as const;
