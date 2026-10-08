// "Connect a source" (docs/adr/0021-source-setup.md): how each setup state is shown, what a
// provider syncs, and plain-language copy for failed sign-ins. GET /providers carries the state.
import type { Problem, Schemas } from '../api/client.ts';
import type { Status } from '../components/StatusIcon.svelte';
import { providerLabel } from '../connections/connections.ts';
import { known, loadProviders } from '../connections/providers.svelte.ts';

export type Provider = Schemas['Provider'];
export type SetupState = Provider['setup_state'];

export const states: Record<SetupState, { status: Status; label: string }> = {
	ready: { status: 'info', label: 'Ready to connect' },
	connected: { status: 'ok', label: 'Connected' },
	needs_app_credentials: { status: 'pending', label: 'Needs its app credentials' },
	needs_public_url: { status: 'warn', label: 'Needs an https public address' },
	needs_sidecar: { status: 'off', label: 'Not available: its sidecar is not running' }
};

/** A provider can be chosen unless the install itself must change first. */
export const choosable = (p: Provider) => p.available && p.setup_state !== 'needs_sidecar' && p.setup_state !== 'needs_public_url';

/** The label of the one next action for a chosen provider. */
export const action = (p: Provider) => (p.setup_state === 'needs_app_credentials' ? `Set up ${p.name}` : `Continue to ${p.name}`);

/** What a known provider brings, shown on its card. */
export const about: Record<string, string> = {
	withings: 'Blood pressure, weight, body composition, activity, intraday heart rate and sleep through the official Withings API.',
	garmin: 'Daily summaries, heart rate, sleep, stress, HRV and activities. The first backfill goes day by day to stay within Garmin’s limits.',
	whoop: 'Heart rate every 6 seconds, cycles, sleep and workouts, paced at one request per second. Heart-rate history goes back 90 days by default.'
};

const repo = 'https://github.com/KaanEmec/vitamux/blob/main/docs/providers';
/** The provider's page in the docs, for the providers that have one. */
export const docsHref = (code: string) => (code in about ? `${repo}/${code}.md` : undefined);

/** Where the owner creates their own provider application, for providers that need one. */
export const appDashboards: Record<string, string> = { withings: 'https://developer.withings.com/dashboard/' };

export const installs = {
	compose: { name: 'Docker Compose', where: 'Add this line to .env' },
	coolify: { name: 'Coolify', where: 'Add this environment variable' }
} as const;

/** Replaces the provider in the shared list (after a probe or a change of its app credentials). */
export function updateProvider(p: Provider) {
	if (known.list) known.list = known.list.map((x) => (x.code === p.code ? p : x));
}

/** "2 minutes" for a Retry-After in seconds; "a minute" when unknown. */
function wait(seconds: number | null): string {
	if (!seconds || seconds <= 60) return 'a minute';
	const m = Math.ceil(seconds / 60);
	return m < 60 ? `${m} minutes` : `${Math.ceil(m / 60)} hours`;
}

/** signInError for a failed begin or continue: on a 503 it checks whether the provider's sidecar went away. */
export async function explain(error: Problem, response: Response, code: string, codeStep = false): Promise<string> {
	let sidecarDown = false;
	if (error.status === 503) {
		await loadProviders(true);
		sidecarDown = known.list?.find((p) => p.code === code)?.setup_state === 'needs_sidecar';
	}
	const retryAfter = Number(response.headers.get('Retry-After')) || null;
	return signInError(error, providerLabel(code), { codeStep, retryAfter, sidecarDown });
}

/**
 * Plain language for a failed sign-in step (POST …/auth/begin or …/auth/continue). `codeStep` is
 * true when the step asked for a verification code; `sidecarDown` when the provider's sidecar no
 * longer answers. The server's own wording stays below it (ProblemAlert `lead`).
 */
function signInError(problem: Problem, name: string, opts: { codeStep?: boolean; retryAfter?: number | null; sidecarDown?: boolean } = {}): string {
	if (opts.sidecarDown) return `The ${name} sidecar is not running, so Vitamux cannot reach ${name}. Turn it on under Connect a source, then start again.`;
	if (problem.status === 429) return `${name} is limiting sign-in attempts. Wait ${wait(opts.retryAfter ?? null)}, then start again.`;
	if (problem.status === 409) return `You signed in to a different ${name} account than this connection uses. Nothing changed.`;
	if (problem.status === 422 || problem.status === 400) {
		if (problem.errors?.some((e) => e.pointer === '/state')) return 'The sign-in took too long or was already used. Start again.';
		return opts.codeStep
			? `${name} did not accept the verification code, or it expired. Start again and use the newest code.`
			: `${name} did not accept the email or password. Check them, then start again.`;
	}
	if (problem.status === 503) return `${name} did not answer. Try again in a few minutes.`;
	return '';
}
