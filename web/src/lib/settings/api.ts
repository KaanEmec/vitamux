// GET and PATCH /settings for the Settings pages. The server's key set grows with the jobs
// that add retention and consent keys, so values stay untyped here and each page reads the
// keys it knows (see the typed controls) while the retention page lists the rest generically.
import { api, type Problem } from '#lib/api/client.ts';

export type SettingsMap = Record<string, unknown>;

export async function loadSettings(): Promise<{ settings: SettingsMap | null; problem: Problem | null }> {
	const { data, error } = await api.GET('/api/v1/settings');
	return error ? { settings: null, problem: error } : { settings: { ...data }, problem: null };
}

/** Merge-patches the given keys and returns the settings the server now holds. */
export async function patchSettings(patch: SettingsMap): Promise<{ settings: SettingsMap | null; problem: Problem | null }> {
	const { data, error } = await api.PATCH('/api/v1/settings', { body: patch });
	return error ? { settings: null, problem: error } : { settings: { ...data }, problem: null };
}
