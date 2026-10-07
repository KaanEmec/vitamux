// Display helpers for connections: provider names, the providers the connect wizard
// offers, auth-error messages and times. Health is shown by components/HealthBadge.svelte.
import type { Schemas } from '../api/client.ts';
import { known } from './providers.svelte.ts';

export { when } from '../settings/format.ts';

export type Connection = Schemas['Connection'];
export type Health = Schemas['Health'];

interface ProviderInfo {
	label: string;
	/** Backfill unit chosen by the connector (shown in the dialog; the server splits). */
	unitDays?: number;
}

// Labels for sources GET /providers does not list (push and manual sources), and backfill units.
const providers: Record<string, ProviderInfo> = {
	withings: { label: 'Withings', unitDays: 30 },
	apple_health: { label: 'Apple Health' },
	manual: { label: 'Manual entries' }
};

/** Streams that run slowly: the server starts at most `perDay` units a day, below the provider's own limit. */
export const paced: Record<string, { perDay: number; note: string }> = {
	'garmin.intraday_reload': {
		perDay: 20,
		note: 'Garmin moves heart rate, steps, stress, sleep and similar detail of older days to cold storage. This asks Garmin to restore one day at a time, then fetches it again. Garmin refuses about 30 requests a day, so each day is its own unit and at most 20 start a day. If Garmin refuses anyway, the rest waits for the next day. It is opt-in and slow: 300 days take about 15 days.'
	}
};

export type Provider = Schemas['Provider'];

/** Providers the connect wizard offers: those a browser can authorize (not file imports or device pairing). */
export const connectable = (providers: Provider[]) =>
	providers.filter((p) => p.auth_kind === null || p.auth_kind === 'oauth2' || p.auth_kind === 'interactive_mfa');

/** The provider's name from GET /providers, else a built-in label, else the code made readable. */
export function providerLabel(code: string): string {
	const listed = known.list?.find((p) => p.code === code);
	if (listed) return listed.name;
	if (providers[code]) return providers[code].label;
	const s = code.replaceAll('_', ' ');
	return s.charAt(0).toUpperCase() + s.slice(1);
}

export function unitDays(code: string): number | undefined {
	return providers[code]?.unitDays;
}

/** How a connection gets its data. */
export const modes: Record<string, string> = { in_process: 'Server sync', push: 'Push uploads', remote: 'Sidecar' };

/** "synced 5 minutes ago", "last upload …" for a push source, "not synced yet". */
export const lastSync = (c: Connection) => (c.last_success_at ? `${c.mode === 'push' ? 'last upload' : 'synced'} ${ago(c.last_success_at)}` : 'not synced yet');

/** Health states that need the owner's attention (shown as alerts on Today). */
export const alerting: Health[] = ['degraded', 'failing', 'needs_reauth', 'stale'];

/** Messages for the OAuth callback's `?auth_error=` codes (internal/api/oauth.go). */
export const authErrors: Record<string, string> = {
	invalid_state: 'The authorization link expired or was already used. Start again.',
	denied: 'Access was not granted at the provider.',
	account_mismatch: 'You signed in to a different account than this connection uses. Nothing changed.',
	exchange_failed: 'The provider did not accept the authorization. Try again later.',
	unavailable: 'The provider or Vitamux could not complete the connection right now. Try again later.'
};

/** A link target from a connector's own description: only http(s), never a script URL. */
export function safeHref(url: string): string | undefined {
	return /^https?:\/\//i.test(url) ? url : undefined;
}

/** Starts the provider's OAuth page: the server answers a redirect URL for this browser. */
export function goToProvider(url: string) {
	window.location.assign(url);
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
const steps: [Intl.RelativeTimeFormatUnit, number][] = [
	['day', 86_400],
	['hour', 3_600],
	['minute', 60]
];

/** "5 minutes ago", "in 2 hours", "never" for null. */
export function ago(iso: string | null | undefined, now = Date.now()): string {
	if (!iso) return 'never';
	const s = (Date.parse(iso) - now) / 1000;
	for (const [unit, size] of steps) {
		if (Math.abs(s) >= size) return rtf.format(Math.round(s / size), unit);
	}
	return 'just now';
}

/** Local date of an instant. */
export function day(iso: string): string {
	return new Date(iso).toLocaleDateString();
}

/** "15 min", "2 h", "1 day" for a duration in seconds. */
export function span(seconds: number): string {
	if (seconds % 86_400 === 0) return `${seconds / 86_400} day${seconds === 86_400 ? '' : 's'}`;
	if (seconds % 3_600 === 0) return `${seconds / 3_600} h`;
	return `${Math.round(seconds / 60)} min`;
}

export const every = (seconds: number) => `every ${span(seconds)}`;

/** "1 min 5 s" between two instants, or "running" while unfinished. */
export function elapsed(from: string, to: string | null): string {
	if (!to) return 'running';
	const s = Math.max(0, Math.round((Date.parse(to) - Date.parse(from)) / 1000));
	return s >= 60 ? `${Math.floor(s / 60)} min ${s % 60} s` : `${s} s`;
}
