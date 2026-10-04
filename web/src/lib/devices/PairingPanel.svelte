<!--
	Pairing: creates a single-use code (valid 10 minutes) and shows it as a QR code plus text, with
	the time left. The server answers 503 without VITAMUX_PUBLIC_URL and 429 after 5 codes in 10 minutes;
	both show as the problem. `oncreated` lets the page refresh its device list when a code is made or expires.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import { countdown } from './format.ts';
	import QrCode from './QrCode.svelte';

	let { onexpired }: { onexpired: () => void } = $props();

	let code = $state<Schemas['PairingCode'] | null>(null);
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	let now = $state(Date.now());

	const left = $derived(code ? Date.parse(code.expires_at) - now : 0);
	const expired = $derived(code !== null && left <= 0);

	onMount(() => {
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(tick);
	});

	// Tell the page once when the code runs out, so it reloads the list a device may have joined.
	let told = '';
	$effect(() => {
		if (code && expired && told !== code.code) {
			told = code.code;
			onexpired();
		}
	});

	async function create() {
		problem = null;
		busy = true;
		const { data, error } = await api.POST('/api/v1/devices/pairing-codes');
		busy = false;
		if (error) {
			problem = error;
			return;
		}
		code = data;
		now = Date.now();
	}
</script>

<ProblemAlert {problem} />
{#if code && !expired}
	<div class="card pairing">
		<QrCode value={code.qr_payload} label="Pairing QR code for {code.url}" />
		<div>
			<p>In the Vitamux app, scan this code, or enter the server address and this code:</p>
			<p class="muted">Server <code>{code.url}</code></p>
			<code class="secret" aria-label="Pairing code">{code.code}</code>
			<p><StatusIcon status="pending" /> Expires in <span role="timer">{countdown(left)}</span>. It works once.</p>
			<button class="btn" type="button" disabled={busy} onclick={create}>New code</button>
		</div>
	</div>
{:else}
	{#if expired}<p><StatusIcon status="off" /> The pairing code expired. Create a new one.</p>{/if}
	<button class="btn primary" type="button" disabled={busy} onclick={create}>Create pairing code</button>
{/if}

<style>
	.pairing {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-5);
		align-items: flex-start;
	}
	.pairing :global(.secret) {
		font-size: var(--text-lg);
		letter-spacing: 0.08em;
	}
</style>
