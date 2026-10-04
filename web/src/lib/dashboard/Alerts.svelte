<!--
	The alert strip: connections that need attention, permanently failed jobs this week, and a
	stale backup. Each alert names what happened and links to where it is handled.
-->
<script lang="ts">
	import type { Schemas } from '../api/client.ts';
	import StatusIcon, { type Status } from '../components/StatusIcon.svelte';
	import { ago, alerting, providerLabel, type Connection } from '../connections/connections.ts';

	interface Alert {
		status: Status;
		text: string;
		href?: string;
		action?: string;
	}

	let {
		connections,
		jobs,
		lastBackup
	}: { connections: Connection[]; jobs: Schemas['Job'][]; lastBackup: string | null } = $props();

	const backupMaxAge = 8 * 86_400_000;

	function connectionAlert(c: Connection): Alert {
		const name = providerLabel(c.provider);
		const href = `/connections/${c.id}`;
		switch (c.health) {
			case 'needs_reauth':
				return { status: 'error', text: `${name} needs reauthorization.`, href, action: `Reauthorize ${name}` };
			case 'failing':
				return {
					status: 'error',
					text: `${name} is failing (${c.consecutive_failures} failed runs${c.last_error_class ? `, ${c.last_error_class}` : ''}).`,
					href: `${href}?tab=history`,
					action: 'See history'
				};
			case 'stale':
				return { status: 'warn', text: `${name} has not synced successfully since ${ago(c.last_success_at)}.`, href, action: 'Open' };
			default:
				return {
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
				status: 'error',
				text: `Job ${j.kind} failed permanently after ${j.attempts} attempts, ${ago(j.finished_at ?? j.created_at)}.`,
				href: j.connection_id ? `/connections/${j.connection_id}?tab=history` : undefined,
				action: j.connection_id ? 'See history' : undefined
			});
		}
		if (jobs.length > 5) out.push({ status: 'error', text: `${jobs.length - 5} more jobs failed permanently this week.` });
		if (lastBackup && Date.now() - Date.parse(lastBackup) > backupMaxAge) {
			out.push({ status: 'warn', text: `The last backup is from ${ago(lastBackup)}.`, href: '/settings/backups', action: 'Backups' });
		}
		return out;
	});
</script>

<section aria-label="Alerts">
	{#if alerts.length}
		<ul>
			{#each alerts as a, i (i)}
				<li class={a.status}>
					<StatusIcon status={a.status} />
					<span>{a.text}</span>
					{#if a.href && a.action}<a href={a.href}>{a.action}</a>{/if}
				</li>
			{/each}
		</ul>
	{:else}
		<p class="quiet"><StatusIcon status="ok" /> Nothing needs your attention.</p>
	{/if}
</section>

<style>
	ul {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	li {
		display: flex;
		flex-wrap: wrap;
		gap: var(--space-2);
		align-items: center;
		min-height: var(--control-h);
		padding: var(--space-2) var(--space-4);
		font-size: var(--text-sm);
		background: var(--color-warn-bg);
		border: 1px solid color-mix(in srgb, var(--color-warn) 40%, transparent);
		border-radius: var(--radius-pill);
	}
	li.error {
		background: var(--color-error-bg);
		border-color: color-mix(in srgb, var(--color-error) 40%, transparent);
	}
	li a {
		font-weight: 600;
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
