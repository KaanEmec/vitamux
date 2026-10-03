// Display helpers for connections: provider names, the providers the connect wizard
// offers, auth-error messages and times. Health is shown by components/HealthBadge.svelte.
import type { Schemas } from '../api/client.ts';

export type Connection = Schemas['Connection'];
export type Health = Schemas['Health'];

interface ProviderInfo {
	label: string;
	/** Backfill unit chosen by the connector (shown in the dialog; the server splits). */
	unitDays?: number;
}

// TODO: replace with a providers endpoint once the API lists registered connectors.
const providers: Record<string, ProviderInfo> = {
	withings: { label: 'Withings', unitDays: 30 },
	apple_health: { label: 'Apple Health' },
	manual: { label: 'Manual entries' }
};

/** Providers with a server-side OAuth connector, offered by the connect wizard. */
export const connectable = [{ code: 'withings', official: true }];

export function providerLabel(code: string): string {
	if (providers[code]) return providers[code].label;
	const s = code.replaceAll('_', ' ');
	return s.charAt(0).toUpperCase() + s.slice(1);
}

export function unitDays(code: string): number | undefined {
	return providers[code]?.unitDays;
}

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

/** Local date and time of an instant, or an en dash. */
export function when(iso: string | null | undefined): string {
	return iso ? new Date(iso).toLocaleString() : '–';
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
