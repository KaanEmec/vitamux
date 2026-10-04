<!--
	Renders a problem+json error. Field errors whose input is listed in `fields` are
	shown by those inputs (see fieldErrors in ../api/client.ts); the rest are listed here.
	`lead` puts plain language first and keeps the server's wording below it.
-->
<script lang="ts">
	import { fieldErrors, type Problem } from '../api/client.ts';
	import StatusIcon from './StatusIcon.svelte';

	let { problem, fields = [], lead = '' }: { problem: Problem | null | undefined; fields?: string[]; lead?: string } = $props();

	const unmapped = $derived(Object.entries(fieldErrors(problem)).filter(([k]) => !fields.includes(k)));
</script>

{#if problem}
	<div class="problem" role="alert">
		<StatusIcon status="error" />
		<div>
			<strong>{lead || problem.detail || problem.title}</strong>
			{#if lead && problem.detail}<div class="detail">{problem.detail}</div>{/if}
			{#if unmapped.length}
				<ul>
					{#each unmapped as [field, detail] (field)}
						<li><code>{field}</code>: {detail}</li>
					{/each}
				</ul>
			{/if}
			{#if problem.request_id}
				<div class="request-id">Request ID: <code>{problem.request_id}</code></div>
			{/if}
		</div>
	</div>
{/if}

<style>
	.problem {
		display: flex;
		gap: var(--space-3);
		align-items: flex-start;
		padding: var(--space-3) var(--space-4);
		margin-bottom: var(--space-4);
		background: var(--color-error-bg);
		border: 1px solid color-mix(in srgb, var(--color-error) 40%, transparent);
		border-radius: var(--radius-md);
	}
	.problem :global(.status-icon) {
		margin-top: 0.2em;
		color: var(--color-error);
	}
	strong {
		font-weight: 600;
	}
	ul {
		margin: var(--space-1) 0 0;
		padding-left: var(--space-5);
	}
	.detail {
		margin-top: var(--space-1);
		font-size: var(--text-sm);
	}
	.request-id {
		margin-top: var(--space-1);
		font-size: var(--text-xs);
		color: var(--color-text-muted);
	}
</style>
