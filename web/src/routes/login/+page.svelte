<!--
	Sign in: username and password, then (when the server answers totp_required) an
	authenticator code or a recovery code. The login API is stateless, so the second step
	resends the password with the code; it is cleared as soon as sign-in succeeds.
-->
<script lang="ts">
	import { tick } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import { safeNext, setSession } from '#lib/session.svelte.ts';

	let step = $state<'password' | 'code'>('password');
	let useRecovery = $state(false);
	let username = $state('');
	let password = $state('');
	let code = $state('');
	let codeInput = $state<HTMLInputElement>();
	let busy = $state(false);
	let problem = $state<Problem | null>(null);

	const fields = $derived(fieldErrors(problem));
	const expired = $derived(page.url.searchParams.get('reason') === 'expired');

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		busy = true;
		problem = null;
		const body: Schemas['LoginRequest'] = { username, password };
		if (step === 'code') {
			if (useRecovery) body.recovery_code = code.trim();
			else body.totp_code = code.replace(/\s/g, '');
		}
		const { data, error, response } = await api.POST('/api/v1/auth/login', { body });
		busy = false;
		if (data) {
			setSession(data);
			password = code = '';
			await goto(safeNext(page.url.searchParams.get('next')), { replaceState: true });
			return;
		}
		if (error.code === 'totp_required' && step === 'password') {
			await showCodeStep(false);
			return;
		}
		const retry = response.headers.get('Retry-After');
		problem = error.code === 'rate_limited' && retry ? { ...error, detail: `${error.detail} (in ${retry} s)` } : error;
	}

	async function showCodeStep(recovery: boolean) {
		step = 'code';
		useRecovery = recovery;
		code = '';
		problem = null;
		await tick();
		codeInput?.focus();
	}

	function back() {
		step = 'password';
		code = password = '';
		problem = null;
	}
</script>

<svelte:head><title>Sign in · Vitamux</title></svelte:head>

<main class="login">
	<h1>Sign in to Vitamux</h1>

	{#if expired}
		<p class="notice" role="status"><StatusIcon status="info" /> Your session has expired. Sign in again to continue.</p>
	{/if}

	<form class="card" onsubmit={submit}>
		<ProblemAlert {problem} fields={['username', 'password', 'totp_code', 'recovery_code']} />

		{#if step === 'password'}
			<TextField
				label="Username"
				name="username"
				bind:value={username}
				error={fields.username}
				autocomplete="username"
				autocapitalize="none"
				spellcheck="false"
				required
				autofocus
			/>
			<TextField
				label="Password"
				name="password"
				type="password"
				bind:value={password}
				error={fields.password}
				autocomplete="current-password"
				required
			/>
		{:else if useRecovery}
			<TextField
				label="Recovery code"
				name="recovery_code"
				bind:value={code}
				bind:input={codeInput}
				error={fields.recovery_code}
				hint="Each recovery code works once."
				autocomplete="off"
				autocapitalize="none"
				spellcheck="false"
				required
			/>
		{:else}
			<TextField
				label="Authenticator code"
				name="totp_code"
				bind:value={code}
				bind:input={codeInput}
				error={fields.totp_code}
				hint="The 6-digit code from your authenticator app."
				inputmode="numeric"
				autocomplete="one-time-code"
				maxlength={8}
				required
			/>
		{/if}

		<div class="actions">
			<button class="btn primary" type="submit" disabled={busy}>{step === 'password' ? 'Sign in' : 'Verify'}</button>
			{#if step === 'code'}
				<button class="btn link" type="button" onclick={() => showCodeStep(!useRecovery)}>
					{useRecovery ? 'Use an authenticator code instead' : 'Use a recovery code instead'}
				</button>
				<button class="btn link" type="button" onclick={back}>Back</button>
			{/if}
		</div>
	</form>
</main>

<style>
	.login {
		max-width: 24rem;
		margin: var(--space-8) auto;
		padding: 0 var(--space-4);
	}
	.notice {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		padding: var(--space-3);
		background: var(--color-info-bg);
		border-radius: var(--radius-sm);
	}
	.actions {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-4);
		align-items: center;
	}
</style>
