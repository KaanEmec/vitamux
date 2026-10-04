// Navigation (docs/architecture/frontend.md#navigation): the six sections in sidebar order,
// the Settings pages, and where a metric opens. Each section is a route under src/routes/(app)/;
// sub-pages live below the section's path. `also` lists other paths that belong to a section.
import { exploreHref } from './explore/links.ts';
import { icons } from './ui/icons.ts';

export const sections = [
	{ href: '/', label: 'Dashboard', icon: icons.dashboard },
	{ href: '/explore', label: 'Explore', icon: icons.explore, also: ['/data'] },
	{ href: '/connections', label: 'Connections', icon: icons.connections },
	{ href: '/rules', label: 'Rules', icon: icons.rules },
	{ href: '/lab', label: 'Lab results', icon: icons.lab },
	{ href: '/settings', label: 'Settings', icon: icons.settings }
] as const;

export const settingsPages = [
	{ href: '/settings', label: 'Profile' },
	{ href: '/settings/devices', label: 'Devices' },
	{ href: '/settings/api-keys', label: 'API keys' },
	{ href: '/settings/ai', label: 'AI providers' },
	{ href: '/settings/retention', label: 'Retention' },
	{ href: '/settings/backups', label: 'Backups and export' },
	{ href: '/settings/security', label: 'Security' },
	{ href: '/settings/system', label: 'System' }
] as const;

/** Where a metric or rule family opens (the command palette, cards): its Explore page. */
export const metricHref = (code: string) => exploreHref({ kind: 'metric', code });

/** True when `pathname` is the section or one of its sub-pages. */
export function inSection(pathname: string, section: { href: string; also?: readonly string[] }): boolean {
	const under = (href: string) => pathname === href || pathname.startsWith(href + '/');
	return section.href === '/' ? pathname === '/' : [section.href, ...(section.also ?? [])].some(under);
}
