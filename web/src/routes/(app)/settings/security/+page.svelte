<!--
	Security: password, two-factor (TOTP) and sessions. Changing the password and listing or
	revoking sessions have no API operation yet (see api/openapi.yaml), so those controls are
	shown disabled; TOTP enrolment, recovery codes and disabling use the auth endpoints.
-->
<script lang="ts">
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { session, setSession } from '#lib/session.svelte.ts';
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

<ProblemAlert {problem} fields={['code', 'password', 'totp_code', 'recovery_code']} />
{#if notice}<Notice>{notice}</Notice>{/if}

<section aria-labelledby="password-h">
	<h2 id="password-h">Password</h2>
	<Notice status="info">Changing the password from the app is not available in this version yet.</Notice>
	<form>
		<fieldset disabled>
			<TextField label="Current password" name="current_password" type="password" autocomplete="current-password" />
			<TextField label="New password" name="new_password" type="password" autocomplete="new-password" />
			<button class="btn primary" type="button">Change password</button>
		</fieldset>
	</form>
</section>

<section aria-labelledby="totp-h">
	<h2 id="totp-h">Two-factor authentication</h2>

	{#if recovery}
		<div class="card">
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
</section>

<section aria-labelledby="sessions-h">
	<h2 id="sessions-h">Sessions</h2>
	<Notice status="info">Listing and revoking other sessions is not available in this version yet. Signing out ends this browser's session.</Notice>
	<button class="btn" type="button" disabled>Sign out other sessions</button>
</section>
