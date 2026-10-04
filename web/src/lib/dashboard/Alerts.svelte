<!--
	The alert strip: connections that need attention, permanently failed jobs this week, and a
	stale backup. Each alert names what happened and links to where it is handled. The owner can
	dismiss one (here only: Connections and Settings still show the state). An alert's key is its
	kind, its subject and when the problem started, so a new occurrence shows again; the dismissed
	keys live in the dashboard layout, and keys of alerts that are gone drop out on the next save.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import { ago, alerting, providerLabel, type Connection } from '../connections/connections.ts';
	import Button from '../ui/Button.svelte';
	import Icon from '../ui/Icon.svelte';
	import { icons } from '../ui/icons.ts';

	interface Alert {
		key: string;
		status: Status;
		text: string;
		href?: string;
		action?: string;
	}

	let {
		connections,
		jobs,
		lastBackup,
		dismissed,
		ondismiss
	}: {
		connections: Connection[];
		jobs: Schemas['Job'][];
		lastBackup: string | null;
		dismissed: string[];
		/** Saves the new list of dismissed keys; without it alerts cannot be dismissed. */
		ondismiss?: (keys: string[]) => void;
	} = $props();

	const backupMaxAge = 8 * 86_400_000;
	const maxKeys = 100; // the layout's bound

	function connectionAlert(c: Connection): Alert {
		const name = providerLabel(c.provider);
		const href = `/connections/${c.id}`;
		// A problem that ends and returns follows a success, so the last success dates it.
		const since = c.last_success_at ?? 'never';
		const key = (kind: string, from = since) => `${kind}:${c.id}:${from}`;
		switch (c.health) {
			case 'needs_reauth':
				return { key: key('reauth'), status: 'error', text: `${name} needs reauthorization.`, href, action: `Reauthorize ${name}` };
			case 'failing':
				return {
					key: key('failing'),
					status: 'error',
					text: `${name} is failing (${c.consecutive_failures} failed runs${c.last_error_class ? `, ${c.last_error_class}` : ''}).`,
					href: `${href}?tab=history`,
					action: 'See history'
				};
			case 'stale':
				return { key: key('stale'), status: 'warn', text: `${name} has not synced successfully since ${ago(c.last_success_at)}.`, href, action: 'Open' };
			default:
				return {
					key: key('degraded', c.health_reason ?? ''),
					status: 'warn',
					text: `${name} is degraded${c.health_reason ? `: ${c.health_reason}` : '.'}`,
					href: `${href}?tab=streams`,
					action: 'See streams'
				};
		}
	}

	const alerts = $derived.by(() => {
		const out: Alert[] = connections.filter((c) => alerting.includes(c.health)).map(connectionAlert);
		for (const j of jobs.slice(0, 5)) {
			out.push({
				key: `job:${j.id}`,
				status: 'error',
				text: `Job ${j.kind} failed permanently after ${j.attempts} attempts, ${ago(j.finished_at ?? j.created_at)}.`,
				href: j.connection_id ? `/connections/${j.connection_id}?tab=history` : undefined,
				action: j.connection_id ? 'See history' : undefined
			});
		}
		if (jobs.length > 5) out.push({ key: `jobs-more:${jobs[5].id}`, status: 'error', text: `${jobs.length - 5} more jobs failed permanently this week.` });
		if (lastBackup && Date.now() - Date.parse(lastBackup) > backupMaxAge) {
			out.push({ key: `backup:${lastBackup}`, status: 'warn', text: `The last backup is from ${ago(lastBackup)}.`, href: '/settings/backups', action: 'Backups' });
		}
		return out;
	});

	let showing = $state(false);
	const open = $derived(alerts.filter((a) => !dismissed.includes(a.key)));
	const closed = $derived(alerts.length - open.length);
	const listed = $derived(showing && closed ? alerts : open);
	// Keys of alerts that are gone are dropped whenever the list is saved.
	const kept = $derived(dismissed.filter((k) => alerts.some((a) => a.key === k)));
	const dismiss = (key: string) => ondismiss?.([...kept, key].slice(-maxKeys));
	const restore = (key: string) => ondismiss?.(kept.filter((k) => k !== key));
</script>

<section aria-label="Alerts">
	{#if listed.length}
		<ul>
			{#each listed as a (a.key)}
				{@const off = dismissed.includes(a.key)}
				<li class={[a.status, off && 'off']}>
					<StatusIcon status={a.status} />
					<span class="text">{a.text}</span>
					{#if a.href && a.action}<a href={a.href}>{a.action}</a>{/if}
					{#if ondismiss}
						{#if off}
							<Button variant="ghost" size="sm" aria-label="Restore: {a.text}" onclick={() => restore(a.key)}>Restore</Button>
						{:else}
							<Button variant="ghost" class="dismiss" aria-label="Dismiss: {a.text}" onclick={() => dismiss(a.key)}>
								<Icon d={icons.close} size={16} />
							</Button>
						{/if}
					{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="quiet"><StatusIcon status="ok" /> {alerts.length ? 'No open alerts.' : 'Nothing needs your attention.'}</p>
	{/if}
	{#if closed}
		<button class="btn link" type="button" aria-expanded={showing} onclick={() => (showing = !showing)}>
			{closed} dismissed · {showing ? 'Hide' : 'Show'}
		</button>
	{/if}
</section>

<style>
	/* One row per alert: what happened, then where it is handled. Tinted by state, never alarming. */
	ul {
		display: grid;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2) var(--space-3);
		align-items: center;
		min-height: var(--control-h);
		padding: var(--space-2) var(--space-4);
		font-size: var(--text-sm);
		background: var(--color-warn-bg);
		border: 1px solid color-mix(in srgb, var(--color-warn) 30%, transparent);
		border-radius: var(--radius-lg);
	}
	li.error {
		background: var(--color-error-bg);
		border-color: color-mix(in srgb, var(--color-error) 30%, transparent);
	}
	.text {
		flex: 1 1 14rem;
	}
	li a {
		font-weight: 500;
		text-decoration: none;
	}
	li.off {
		border-style: dashed;
	}
	/* The icon button keeps a 44 px target without growing the row. */
	li :global(.dismiss) {
		min-width: var(--control-h);
		min-height: var(--control-h);
		margin: calc(var(--space-2) * -1) calc(var(--space-3) * -1) calc(var(--space-2) * -1) 0;
		padding: 0;
		color: var(--color-text-muted);
	}
	.btn.link {
		min-height: var(--control-h);
		margin-top: var(--space-1);
		font-size: var(--text-sm);
	}
	.quiet {
		display: flex;
		gap: var(--space-2);
		align-items: center;
		margin: 0;
		font-size: var(--text-sm);
		color: var(--color-text-muted);
	}
</style>
