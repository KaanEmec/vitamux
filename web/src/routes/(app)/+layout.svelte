<!--
	App shell: skip link, section navigation, signed-in user and sign-out, main region.
	Pages render <svelte:head><title>X · Vitamux</title></svelte:head> and one <h1>.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem } from '#lib/api/client.ts';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { sections, inSection } from '#lib/nav.ts';
	import { clearSession, session } from '#lib/session.svelte.ts';

	let { children } = $props();

	let version = $state('');
	let signOutProblem = $state<Problem | null>(null);

	onMount(() => {
		api.GET('/api/v1/system/version').then(({ data }) => {
			if (data) version = data.version;
		});
	});

	async function signOut() {
		signOutProblem = null;
		const { error, response } = await api.POST('/api/v1/auth/logout');
		if (error && response.status !== 401) {
			signOutProblem = error;
			return;
		}
		clearSession();
		await goto('/login', { replaceState: true });
	}
</script>

<a class="skip-link" href="#main">Skip to content</a>

<div class="shell">
	<header class="sidebar">
		<a class="brand" href="/">Vitamux</a>
		<nav aria-label="Sections">
			<ul>
				{#each sections as s (s.href)}
					<li>
						<a href={s.href} aria-current={inSection(page.url.pathname, s.href) ? 'page' : undefined}>{s.label}</a>
					</li>
				{/each}
			</ul>
		</nav>
		<div class="account">
			{#if session.user}<span class="muted">Signed in as <strong>{session.user.username}</strong></span>{/if}
			<button class="btn" type="button" onclick={signOut}>Sign out</button>
		</div>
		{#if version}<div class="version muted">v{version}</div>{/if}
	</header>

	<main id="main" tabindex="-1">
		<ProblemAlert problem={signOutProblem} />
		{@render children()}
	</main>
</div>

<style>
	.skip-link {
		position: absolute;
		left: var(--space-2);
		top: -10rem;
		z-index: 10;
		padding: var(--space-2) var(--space-4);
		background: var(--color-surface);
		border-radius: var(--radius-sm);
	}
	.skip-link:focus {
		top: var(--space-2);
	}

	.shell {
		display: grid;
		grid-template-columns: var(--sidebar-width) 1fr;
		min-height: 100vh;
	}
	.sidebar {
		display: flex;
		flex-direction: column;
		gap: var(--space-4);
		padding: var(--space-4);
		background: var(--color-surface);
		border-right: 1px solid var(--color-border);
	}
	.brand {
		font-weight: 700;
		font-size: var(--text-lg);
		color: var(--color-text);
		text-decoration: none;
	}
	nav ul {
		display: flex;
		flex-direction: column;
		gap: var(--space-1);
		margin: 0;
		padding: 0;
		list-style: none;
	}
	nav a {
		display: block;
		padding: var(--space-2) var(--space-3);
		color: var(--color-text);
		text-decoration: none;
		border-radius: var(--radius-sm);
	}
	nav a:hover {
		background: var(--color-surface-2);
	}
	nav a[aria-current='page'] {
		font-weight: 600;
		color: var(--color-accent);
		background: var(--color-surface-2);
		box-shadow: inset 3px 0 0 var(--color-accent);
	}
	.account {
		display: grid;
		gap: var(--space-2);
		justify-items: start;
		margin-top: auto;
		font-size: var(--text-sm);
	}
	.version {
		font-size: var(--text-xs);
	}
	main {
		width: 100%;
		max-width: var(--content-max);
		padding: var(--space-5) var(--space-6);
	}
	main:focus {
		outline: none;
	}

	@media (max-width: 48rem) {
		.shell {
			grid-template-columns: 1fr;
		}
		.sidebar {
			flex-direction: row;
			flex-wrap: wrap;
			align-items: center;
			border-right: 0;
			border-bottom: 1px solid var(--color-border);
		}
		nav ul {
			flex-direction: row;
			flex-wrap: wrap;
		}
		.account {
			grid-auto-flow: column;
			align-items: center;
			margin: 0 0 0 auto;
		}
		.version {
			display: none;
		}
		main {
			padding: var(--space-4);
		}
	}
</style>
