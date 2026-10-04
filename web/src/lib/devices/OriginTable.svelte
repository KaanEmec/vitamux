<!--
	Every app (origin) data was recorded by, with its state: native, relayed from a vendor, or direct.
	The owner sets or clears the vendor an origin relays; rules and the all-sources view read it at
	once (apple-health.md#origins-and-relays). Native origins are fixed.
-->
<script lang="ts">
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import StatusIcon from '#lib/components/StatusIcon.svelte';
	import Notice from '#lib/settings/Notice.svelte';

	let {
		origins,
		targets,
		onchanged
	}: { origins: Schemas['DataOrigin'][]; targets: Schemas['RelayTarget'][]; onchanged: () => Promise<void> } = $props();

	let problem = $state<Problem | null>(null);
	let busy = $state(false);
	let saved = $state('');

	const label = (o: Schemas['DataOrigin']) => o.name || o.origin_key;
	const targetName = (code: string) => targets.find((t) => t.code === code)?.name ?? code;

	async function classify(o: Schemas['DataOrigin'], value: string) {
		problem = null;
		saved = '';
		busy = true;
		const { error } = await api.PATCH('/api/v1/origins/{id}', {
			params: { path: { id: o.id } },
			body: { relayed_provider: value || null }
		});
		busy = false;
		if (error) problem = error;
		else saved = value ? `${label(o)} now relays ${targetName(value)}.` : `${label(o)} now records its own data.`;
		await onchanged();
	}
</script>

<ProblemAlert {problem} />
{#if saved}<Notice>{saved} Resolved values are being recomputed.</Notice>{/if}
{#if origins.length === 0}
	<p class="muted">No origins yet. They appear once a device has synced.</p>
{:else}
	<div class="table-wrap">
		<table>
			<caption class="visually-hidden">Origins</caption>
			<thead>
				<tr><th scope="col">App</th><th scope="col">Transport</th><th scope="col">State</th><th scope="col">Relays</th></tr>
			</thead>
			<tbody>
				{#each origins as o (o.id)}
					<tr>
						<th scope="row">{label(o)}{#if o.name}<br /><code class="muted">{o.origin_key}</code>{/if}</th>
						<td>{o.provider}</td>
						<td>
							{#if o.is_native}<StatusIcon status="ok" /> Native
							{:else if o.relayed_provider}<StatusIcon status="info" /> Relayed from {targetName(o.relayed_provider)}
							{:else}<StatusIcon status="off" /> Direct{/if}
						</td>
						<td>
							{#if o.is_native}
								–
							{:else}
								<select
									aria-label="Vendor relayed by {label(o)}"
									value={o.relayed_provider ?? ''}
									disabled={busy}
									onchange={(e) => classify(o, e.currentTarget.value)}
								>
									<option value="">Nothing (records its own data)</option>
									{#each targets as t (t.code)}<option value={t.code}>{t.name}</option>{/each}
								</select>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
{/if}
