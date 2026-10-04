<!--
	Profile: the signed-in owner, timezone periods (every local date is computed from the
	period in effect, so adding or changing one recomputes the affected days) and the
	Withings notification toggle.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { api, fieldErrors, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import TextField from '#lib/components/TextField.svelte';
	import Card from '#lib/settings/Card.svelte';
	import { session } from '#lib/session.svelte.ts';
	import { loadSettings, patchSettings } from '#lib/settings/api.ts';
	import Notice from '#lib/settings/Notice.svelte';
	import { validZone, zoneNames, zonedInstant, zonedLabel, zonedLocal } from '#lib/settings/tz.ts';

	type Period = Schemas['TimezonePeriod'];

	let periods = $state<Period[] | null>(null);
	let problem = $state<Problem | null>(null);
	let notice = $state('');
	let busy = $state(false);

	let editing = $state<string | null>(null);
	let removing = $state<string | null>(null);
	let tz = $state('');
	let from = $state('');
	let local = $state<Record<string, string>>({});
	const errors = $derived({ ...fieldErrors(problem), ...local });
	const zones = zoneNames();

	let withings = $state(false);
	let withingsProblem = $state<Problem | null>(null);
	let withingsSaved = $state(false);

	async function load() {
		const { data, error } = await api.GET('/api/v1/timezone-periods');
		if (error) problem = error;
		else periods = data.timezone_periods;
	}

	onMount(() => {
		void load();
		void loadSettings().then(({ settings, problem: p }) => {
			withingsProblem = p;
			withings = settings?.['withings.notifications'] === true;
		});
	});

	function reset() {
		editing = null;
		tz = '';
		from = '';
		local = {};
	}

	function edit(p: Period) {
		reset();
		notice = '';
		problem = null;
		editing = p.id;
		tz = p.tz;
		from = zonedLocal(p.valid_from, p.tz);
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		problem = null;
		notice = '';
		local = {};
		const zone = tz.trim();
		const known = validZone(zone);
		if (!known) local.tz = 'Enter an IANA timezone such as Europe/Berlin.';
		const instant = known ? zonedInstant(from, zone) : null;
		if (known && !instant) local.valid_from = 'Enter the date and time the period starts.';
		if (!instant) return;

		busy = true;
		const body = { tz: zone, valid_from: instant };
		const res = editing
			? await api.PATCH('/api/v1/timezone-periods/{id}', { params: { path: { id: editing } }, body })
			: await api.POST('/api/v1/timezone-periods', { body });
		busy = false;
		if (res.error) {
			problem = res.error;
			return;
		}
		notice = `Saved. Local dates from ${zonedLabel(instant, zone)} (${zone}) onward are being recomputed; affected days update shortly.`;
		reset();
		await load();
	}

	async function remove(p: Period) {
		busy = true;
		problem = null;
		notice = '';
		const { error } = await api.DELETE('/api/v1/timezone-periods/{id}', { params: { path: { id: p.id } } });
		busy = false;
		removing = null;
		if (error) {
			problem = error;
			return;
		}
		notice = `Removed the ${p.tz} period. The previous period now extends over it and local dates are being recomputed.`;
		await load();
	}

	async function saveWithings(e: SubmitEvent) {
		e.preventDefault();
		withingsSaved = false;
		const { settings, problem: p } = await patchSettings({ 'withings.notifications': withings });
		withingsProblem = p;
		if (settings) {
			withings = settings['withings.notifications'] === true;
			withingsSaved = true;
		}
	}
</script>

<svelte:head><title>Settings · Vitamux</title></svelte:head>

<p class="lede">The signed-in owner, the time zones your local dates follow, and Withings notifications.</p>

<Card title="Account" id="account">
	<p>Signed in as <strong>{session.user?.username ?? '–'}</strong>. Password and two-factor are under <a href="/settings/security">Security</a>.</p>
</Card>

<Card
	title="Time zones"
	id="periods"
	description="Local dates (days, nights, sleep) follow the timezone in effect when a value was measured. Add a period when you move; changing one recomputes the local dates it covers."
>
	{#if notice}<Notice>{notice}</Notice>{/if}
	<ProblemAlert {problem} fields={['tz', 'valid_from']} />

	{#if periods === null}
		<p class="muted" role="status">Loading timezone periods…</p>
	{:else if periods.length === 0}
		<p class="muted">No timezone periods yet. Add the one you are in now.</p>
	{:else}
		<div class="table-wrap">
			<table>
				<caption class="visually-hidden">Timezone periods, oldest first</caption>
				<thead>
					<tr><th scope="col">Timezone</th><th scope="col">From</th><th scope="col">Until</th><th scope="col"><span class="visually-hidden">Actions</span></th></tr>
				</thead>
				<tbody>
					{#each periods as p (p.id)}
						<tr>
							<th scope="row">{p.tz}</th>
							<td>{zonedLabel(p.valid_from, p.tz)}</td>
							<td>{p.valid_to ? zonedLabel(p.valid_to, p.tz) : 'Current'}</td>
							<td>
								<div class="actions">
									{#if removing === p.id}
										<span>Remove {p.tz}?</span>
										<button class="btn sm" type="button" disabled={busy} onclick={() => remove(p)}>Confirm remove</button>
										<button class="btn sm" type="button" onclick={() => (removing = null)}>Keep</button>
									{:else}
										<button class="btn sm" type="button" onclick={() => edit(p)} aria-label="Edit {p.tz} period">Edit</button>
										<button class="btn sm" type="button" onclick={() => (removing = p.id)} aria-label="Remove {p.tz} period">Remove</button>
									{/if}
								</div>
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}

	<form class="callout" onsubmit={submit} aria-labelledby="period-form">
		<h4 id="period-form">{editing ? 'Change period' : 'Add a period'}</h4>
		<div class="row-form">
			<TextField label="Timezone" name="tz" bind:value={tz} error={errors.tz} list="zone-names" autocomplete="off" required />
			<TextField
				label="Starts at"
				name="valid_from"
				bind:value={from}
				error={errors.valid_from}
				hint="Wall-clock time in that timezone."
				type="datetime-local"
				required
			/>
			<div class="actions">
				<button class="btn primary" type="submit" disabled={busy}>{editing ? 'Save period' : 'Add period'}</button>
				{#if editing}<button class="btn" type="button" onclick={reset}>Cancel</button>{/if}
			</div>
		</div>
		<datalist id="zone-names">
			{#each zones as z (z)}<option value={z}></option>{/each}
		</datalist>
	</form>
</Card>

<Card title="Withings notifications" id="withings">
	<form onsubmit={saveWithings}>
		<ProblemAlert problem={withingsProblem} />
		{#if withingsSaved}<Notice>Saved.</Notice>{/if}
		<label class="check">
			<input type="checkbox" name="withings_notifications" bind:checked={withings} onchange={() => (withingsSaved = false)} />
			<span>
				Subscribe to Withings notifications
				<span class="hint">New measurements arrive sooner. Polling runs either way. Needs the public URL configured for the server.</span>
			</span>
		</label>
		<button class="btn primary" type="submit">Save</button>
	</form>
</Card>

<style>
	h4 {
		margin: 0 0 var(--space-3);
		font-size: var(--text-md);
	}
</style>
