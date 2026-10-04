<!--
	Security: password, two-factor (TOTP) and sessions, all through the session-only auth
	endpoints. Changing the password signs out every other session.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { session, setSession } from '#lib/session.svelte.ts';
	import { when } from '#lib/settings/format.ts';
	import Card from '#lib/settings/Card.svelte';
	import Notice from '#lib/settings/Notice.svelte';

	let problem = $state<Problem | null>(null);
	let notice = $state('');
	let busy = $state(false);
	const errors = $derived(fieldErrors(problem));

	let enrolment = $state<Schemas['TOTPEnrollment'] | null>(null);
	let code = $state('');
	let recovery = $state<string[] | null>(null);

	let password = $state('');
	let totpCode = $state('');
	let recoveryCode = $state('');
	let disabling = $state(false);

	const enabled = $derived(session.user?.totp_enabled === true);

	let currentPassword = $state('');
	let newPassword = $state('');
	let sessions = $state<Schemas['ActiveSession'][] | null>(null);
	const others = $derived((sessions ?? []).filter((s) => !s.current));

	async function loadSessions() {
		const { data, error } = await api.GET('/api/v1/auth/sessions');
		if (error) problem = error;
		else sessions = data.sessions;
	}
	onMount(() => void loadSessions());

	async function changePassword(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		notice = '';
		busy = true;
		const { error } = await api.POST('/api/v1/auth/password', { body: { current_password: currentPassword, new_password: newPassword } });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		currentPassword = newPassword = '';
		notice = 'Password changed. Every other session was signed out.';
		await loadSessions();
	}

	async function revoke(ids: string[], done: string) {
		problem = null;
		notice = '';
		busy = true;
		for (const id of ids) {
			const { error } = await api.DELETE('/api/v1/auth/sessions/{id}', { params: { path: { id } } });
			if (error) {
				problem = error;
				break;
			}
		}
		busy = false;
		if (!problem) notice = done;
		await loadSessions();
	}

	async function refreshSession() {
		const { data } = await api.GET('/api/v1/auth/session');
		if (data) setSession(data);
	}

	async function enroll() {
		problem = null;
		notice = '';
		busy = true;
		const { data, error } = await api.POST('/api/v1/auth/totp/enroll');
		busy = false;
		if (error) problem = error;
		else enrolment = data;
	}

	async function confirm(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		busy = true;
		const { data, error } = await api.POST('/api/v1/auth/totp/confirm', { body: { code: code.trim() } });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		recovery = data.recovery_codes;
		enrolment = null;
		code = '';
		await refreshSession();
	}

	async function disable(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		notice = '';
		busy = true;
		const body: { password: string; totp_code?: string; recovery_code?: string } = { password };
		if (totpCode.trim()) body.totp_code = totpCode.trim();
		if (recoveryCode.trim()) body.recovery_code = recoveryCode.trim();
		const { error } = await api.POST('/api/v1/auth/totp/disable', { body });
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		password = totpCode = recoveryCode = '';
		disabling = false;
		notice = 'Two-factor authentication is off.';
		await refreshSession();
	}
</script>

<svelte:head><title>Security · Vitamux</title></svelte:head>

<p class="lede">Your password, two-factor authentication and the sessions signed in to this server.</p>

<ProblemAlert {problem} fields={['code', 'password', 'totp_code', 'recovery_code', 'current_password', 'new_password']} />
{#if notice}<Notice>{notice}</Notice>{/if}

<Card title="Password" id="password-h" description="Changing the password signs out every other session.">
	<form onsubmit={changePassword}>
		<TextField label="Current password" name="current_password" type="password" bind:value={currentPassword} error={errors.current_password} autocomplete="current-password" required />
		<TextField label="New password" name="new_password" type="password" bind:value={newPassword} error={errors.new_password} hint="At least 12 characters. Other sessions are signed out." autocomplete="new-password" minlength={12} required />
		<button class="btn primary" type="submit" disabled={busy}>Change password</button>
	</form>
</Card>

<Card title="Two-factor authentication" id="totp-h">
	{#if recovery}
		<div class="callout">
			<strong>Recovery codes</strong>
			<p>Save these codes somewhere safe. Each works once if you lose your authenticator, and they are not shown again.</p>
			<ul class="secret" aria-label="Recovery codes">
				{#each recovery as c (c)}<li><code>{c}</code></li>{/each}
			</ul>
			<button class="btn primary" type="button" onclick={() => (recovery = null)}>I have saved them</button>
		</div>
	{/if}

	{#if enabled}
		<p><StatusIcon status="ok" /> Two-factor authentication is on.</p>
		{#if disabling}
			<form onsubmit={disable}>
				<p class="muted">Enter your password and a code from your authenticator, or one recovery code.</p>
				<TextField label="Password" name="password" type="password" bind:value={password} error={errors.password} autocomplete="current-password" required />
				<TextField label="Authenticator code" name="totp_code" bind:value={totpCode} error={errors.totp_code} inputmode="numeric" autocomplete="one-time-code" />
				<TextField label="Or a recovery code" name="recovery_code" bind:value={recoveryCode} error={errors.recovery_code} autocomplete="off" />
				<div class="actions">
					<button class="btn primary" type="submit" disabled={busy}>Turn off two-factor</button>
					<button class="btn" type="button" onclick={() => (disabling = false)}>Cancel</button>
				</div>
			</form>
		{:else}
			<button class="btn" type="button" onclick={() => (disabling = true)}>Turn off…</button>
		{/if}
	{:else if enrolment}
		<form onsubmit={confirm}>
			<p>Add this key to your authenticator app (enter it manually, or open the link on a device that has one), then enter the code it shows.</p>
			<code class="secret" aria-label="Setup key">{enrolment.secret}</code>
			<p><a href={enrolment.otpauth_uri}>Open in an authenticator app</a></p>
			<TextField label="Code from the app" name="code" bind:value={code} error={errors.code} inputmode="numeric" autocomplete="one-time-code" required />
			<div class="actions">
				<button class="btn primary" type="submit" disabled={busy}>Confirm and turn on</button>
				<button class="btn" type="button" onclick={() => (enrolment = null)}>Cancel</button>
			</div>
		</form>
	{:else}
		<p><StatusIcon status="off" /> Two-factor authentication is off.</p>
		<button class="btn primary" type="button" onclick={enroll} disabled={busy}>Set up two-factor</button>
	{/if}
</Card>

<Card title="Sessions" id="sessions-h">
	{#snippet aside()}
		<button class="btn sm" type="button" onclick={() => revoke(others.map((s) => s.id), 'Other sessions signed out.')} disabled={busy || others.length === 0}>Sign out other sessions</button>
	{/snippet}
	{#if sessions === null}
		<p class="muted" role="status">Loading sessions…</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">Sessions</caption>
				<thead>
					<tr><th scope="col">Signed in</th><th scope="col">Last active</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr>
				</thead>
				<tbody>
					{#each sessions as s (s.id)}
						<tr>
							<td>{when(s.created_at)}</td>
							<td>{when(s.last_seen_at)}</td>
							<td>
								{#if s.current}This browser{:else}
									<button class="btn sm" type="button" onclick={() => revoke([s.id], 'Session signed out.')} disabled={busy} aria-label="Sign out session from {when(s.created_at)}">Sign out</button>
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</Card>
