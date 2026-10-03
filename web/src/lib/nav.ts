// The six top-level sections (docs/architecture/frontend.md#navigation), in nav order.
// Each is a route under src/routes/(app)/; sub-pages live below the section's path.
export const sections = [
	{ href: '/', label: 'Today' },
	{ href: '/connections', label: 'Connections' },
	{ href: '/data', label: 'Data' },
	{ href: '/rules', label: 'Rules' },
	{ href: '/lab', label: 'Lab results' },
	{ href: '/settings', label: 'Settings' }
] as const;

/** True when `pathname` is the section or one of its sub-pages. */
export function inSection(pathname: string, href: string): boolean {
	return href === '/' ? pathname === '/' : pathname === href || pathname.startsWith(href + '/');
}
