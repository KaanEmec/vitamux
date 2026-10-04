<!--
	Generic auth steps of a connector (docs/architecture/connectors.md#oauth-connection-flow):
	renders the owner prompt of a step, sends the values to POST /providers/{provider}/auth/continue
	and follows the answer until the connection exists: another prompt is shown here, a redirect
	leaves for the provider, a connection id opens the connection page (or calls `ondone`, for a
	page that already shows the connection). The server consumes a
	state when it is continued, so after an error the owner starts again (`onrestart`).
	Entered values are cleared the moment they are sent and are never kept anywhere else.
-->
<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, type Problem, type Schemas } from '../api/client.ts';
	import ProblemAlert from '../components/ProblemAlert.svelte';
	import TextField from '../components/TextField.svelte';
	import { goToProvider } from './connections.ts';

	type Step = Schemas['AuthPromptStep'];
	type Field = Step['prompt']['fields'][number];

	let {
		provider,
		step: first,
		onrestart,
		ondone
	}: { provider: string; step: Step; onrestart: () => void; ondone?: (connectionId: string) => void } = $props();

	// The first step only seeds the loop; later steps come from the server's answers.
	// svelte-ignore state_referenced_locally
	let step = $state<Step>(first);
	const blank = (s: Step): Record<string, string> => Object.fromEntries(s.prompt.fields.map((f) => [f.name, '']));
	// svelte-ignore state_referenced_locally
	let values = $state(blank(first));
	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	let form = $state<HTMLFormElement>();

	// Focus the first field of every step, since the previous step's button is gone.
	$effect(() => {
		void step.state;
		form?.querySelector('input')?.focus();
	});

	const attrs = (f: Field) =>
		f.kind === 'password'
			? ({ type: 'password', autocomplete: 'off' } as const)
			: f.kind === 'code'
				? ({ type: 'text', inputmode: 'numeric', autocomplete: 'one-time-code' } as const)
				: ({ type: 'text', autocomplete: 'off' } as const);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		problem = null;
		const sent = { ...values };
		values = blank(step);
		const { data, error } = await api.POST('/api/v1/providers/{provider}/auth/continue', {
			params: { path: { provider } },
			body: { state: step.state, values: sent }
		});
		if (error) {
			problem = error;
			busy = false;
		} else if ('connection_id' in data) {
			if (ondone) ondone(data.connection_id);
			else await goto(`/connections/${data.connection_id}`);
		} else if ('redirect_url' in data) {
			goToProvider(data.redirect_url);
		} else {
			step = data;
			values = blank(data);
			busy = false;
		}
	}
</script>

{#if problem}
	<ProblemAlert {problem} />
	<button class="btn primary" type="button" onclick={onrestart}>Start again</button>
{:else}
	{#key step.state}
		<form bind:this={form} onsubmit={submit}>
			<p>{step.prompt.message}</p>
			{#each step.prompt.fields as f (f.name)}
				<TextField label={f.label} name={f.name} bind:value={values[f.name]} required {...attrs(f)} />
			{/each}
			<button class="btn primary" type="submit" disabled={busy}>Continue</button>
		</form>
	{/key}
{/if}
