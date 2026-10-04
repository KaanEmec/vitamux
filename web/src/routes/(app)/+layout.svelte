<!--
	App shell (docs/architecture/frontend.md#navigation): skip link, collapsible icon sidebar with
	the sections and the signed-in user, a top bar with the ⌘K command palette, the sync-status
	pill, the theme toggle and sign-out, and the main region. Below 48rem the sidebar becomes a
	bottom bar whose "More" opens the full menu.
	Pages render <svelte:head><title>X · Vitamux</title></svelte:head> and one <h1>.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api, type Problem, type Schemas } from '#lib/api/client.ts';
	import Modal from '#lib/components/Modal.svelte';
	import ProblemAlert from '#lib/components/ProblemAlert.svelte';
	import { inSection, sections } from '#lib/nav.ts';
	import { prefs, setTheme, toggleSidebar, type Theme } from '#lib/prefs.svelte.ts';
	import { clearSession, session } from '#lib/session.svelte.ts';
	import CommandPalette from '#lib/shell/CommandPalette.svelte';
	import NavList from '#lib/shell/NavList.svelte';
	import SyncStatus from '#lib/shell/SyncStatus.svelte';
	import Button from '#lib/ui/Button.svelte';
	import Icon from '#lib/ui/Icon.svelte';
	import Logo from '#lib/ui/Logo.svelte';
	import { icons } from '#lib/ui/icons.ts';
	import Segmented from '#lib/ui/Segmented.svelte';

	let { children } = $props();

	let version = $state('');
	let connections = $state<Schemas['Connection'][]>([]);
	let signOutProblem = $state<Problem | null>(null);
	let palette = $state(false);
	let menu = $state(false);

	const mac = /Mac|iPhone|iPad/.test(navigator.userAgent);
	const themes: { value: Theme; label: string; icon: string }[] = [
		{ value: 'system', label: 'Use system theme', icon: icons.system },
		{ value: 'light', label: 'Light theme', icon: icons.light },
		{ value: 'dark', label: 'Dark theme', icon: icons.dark }
	];
	const mobileTabs = [sections[0], sections[1], { ...sections[2], label: 'Sources' }];

	onMount(() => {
		api.GET('/api/v1/system/version').then(({ data }) => {
			if (data) version = data.version;
		});
		api.GET('/api/v1/connections').then(({ data }) => {
			if (data) connections = data.connections;
		});
	});
	afterNavigate(() => (menu = false));

	function keydown(e: KeyboardEvent) {
		if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
			e.preventDefault();
			palette = true;
		}
	}

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

<svelte:window onkeydown={keydown} />

{#snippet account()}
	{#if session.user}<span class="muted">Signed in as <strong>{session.user.username}</strong></span>{/if}
	{#if version}<span class="version">Vitamux v{version} · self-hosted</span>{/if}
{/snippet}

<a class="skip-link" href="#main">Skip to content</a>

<div class={['shell', prefs.sidebarCollapsed && 'collapsed']}>
	<header class="sidebar">
		<a class="brand" href="/">
			<Logo />
			<span class={{ 'visually-hidden': prefs.sidebarCollapsed }}>Vitamux</span>
		</a>
		<NavList label="Sections" compact={prefs.sidebarCollapsed} />
		<div class="foot">
			{#if !prefs.sidebarCollapsed}<div class="account">{@render account()}</div>{/if}
			<Button
				variant="ghost"
				size="sm"
				icon={icons.sidebar}
				aria-label={prefs.sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'}
				aria-expanded={!prefs.sidebarCollapsed}
				onclick={toggleSidebar}
			/>
		</div>
	</header>

	<div class="column">
		<div class="topbar">
			<button
				class="btn lg jump"
				type="button"
				aria-label="Jump to a metric, source, rule or setting"
				aria-keyshortcuts={mac ? 'Meta+K' : 'Control+K'}
				onclick={() => (palette = true)}
			>
				<Icon d={icons.search} size={16} />
				<span class="jump-label" aria-hidden="true">Jump to a metric, source, rule or setting</span>
				<kbd>{mac ? '⌘K' : 'Ctrl K'}</kbd>
			</button>
			<span class="spacer"></span>
			<SyncStatus {connections} />
			<span class="theme"><Segmented label="Theme" options={themes} value={prefs.theme} onchange={setTheme} /></span>
			<Button size="lg" icon={icons.signOut} aria-label="Sign out" title="Sign out" onclick={signOut} />
		</div>

		<main id="main" tabindex="-1">
			<ProblemAlert problem={signOutProblem} />
			{@render children()}
		</main>
	</div>
</div>

<nav class="bottom-bar" aria-label="Quick sections">
	{#each mobileTabs as s (s.href)}
		<a href={s.href} aria-current={inSection(page.url.pathname, s) ? 'page' : undefined}><Icon d={s.icon} size={22} />{s.label}</a>
	{/each}
	<button type="button" onclick={() => (menu = true)}><Icon d={icons.more} size={22} />More</button>
</nav>

{#if menu}
	<Modal title="Menu" drawer="left" onclose={() => (menu = false)}>
		<NavList label="All sections" />
		<div class="menu-extra">
			<Segmented label="Theme" options={themes} value={prefs.theme} onchange={setTheme} />
			<div class="account">{@render account()}</div>
		</div>
	</Modal>
{/if}

{#if palette}
	<CommandPalette {connections} onclose={() => (palette = false)} />
{/if}

<style>
	.skip-link {
		position: absolute;
		left: var(--space-2);
		top: -10rem;
		z-index: 30;
		padding: var(--space-2) var(--space-4);
		background: var(--color-surface);
		border-radius: var(--radius-sm);
	}
	.skip-link:focus {
		top: var(--space-2);
	}

	.shell {
		display: grid;
		grid-template-columns: var(--sidebar-width) minmax(0, 1fr);
		min-height: 100vh;
	}
	.shell.collapsed {
		grid-template-columns: var(--sidebar-collapsed) minmax(0, 1fr);
	}

	.sidebar {
		position: sticky;
		top: 0;
		display: flex;
		flex-direction: column;
		gap: var(--space-5);
		height: 100vh;
		padding: var(--space-4) var(--space-3);
		overflow-y: auto;
		background: var(--color-sidebar);
		border-right: 1px solid var(--color-border);
	}
	.brand {
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-1) var(--space-2);
		font-size: var(--text-lg);
		font-weight: 650;
		color: var(--color-text);
		text-decoration: none;
	}
	.collapsed .brand {
		justify-content: center;
		padding: var(--space-1) 0;
	}
	.foot {
		display: flex;
		align-items: flex-end;
		gap: var(--space-2);
		margin-top: auto;
	}
	.collapsed .foot {
		justify-content: center;
	}
	.account {
		display: grid;
		flex: 1;
		gap: var(--space-1);
		font-size: var(--text-xs);
	}
	.version {
		color: var(--color-text-muted);
	}
	.collapsed .foot :global(.icon) {
		transform: scaleX(-1);
	}

	.column {
		display: flex;
		flex-direction: column;
		min-width: 0;
	}
	.topbar {
		position: sticky;
		top: 0;
		z-index: 20;
		display: flex;
		align-items: center;
		gap: var(--space-3);
		padding: var(--space-3) var(--space-6);
		background: color-mix(in srgb, var(--color-bg) 88%, transparent);
		border-bottom: 1px solid var(--color-border);
		backdrop-filter: blur(8px);
	}
	.btn.jump {
		display: flex;
		flex: 1 1 16rem;
		align-items: center;
		gap: var(--space-3);
		justify-content: flex-start;
		max-width: 28rem;
		padding: 0 var(--space-3);
		font-size: var(--text-sm);
		font-weight: 400;
		color: var(--color-text-muted);
		text-align: left;
	}
	.jump-label {
		flex: 1;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	kbd {
		padding: 1px var(--space-2);
		font-size: var(--text-2xs);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-xs);
	}
	.spacer {
		flex: 1;
	}

	main {
		width: 100%;
		max-width: var(--content-max);
		padding: var(--space-6) var(--space-6) var(--space-8);
	}
	main:focus {
		outline: none;
	}

	.bottom-bar {
		display: none;
	}
	.menu-extra {
		display: grid;
		justify-items: start;
		gap: var(--space-4);
		margin-top: var(--space-5);
		padding-top: var(--space-4);
		border-top: 1px solid var(--color-border);
	}

	@media (max-width: 48rem) {
		.shell,
		.shell.collapsed {
			grid-template-columns: minmax(0, 1fr);
		}
		.sidebar,
		.theme,
		.jump-label,
		.jump kbd {
			display: none;
		}
		.topbar {
			padding: var(--space-2) var(--space-4);
		}
		.btn.jump {
			flex: none;
			justify-content: center;
			width: var(--control-h);
			padding: 0;
		}
		main {
			padding: var(--space-4) var(--space-4) calc(var(--space-8) + 4.5rem);
		}
		.bottom-bar {
			position: fixed;
			inset: auto 0 0;
			z-index: 20;
			display: grid;
			grid-template-columns: repeat(4, minmax(0, 1fr));
			height: 4.5rem;
			padding-bottom: env(safe-area-inset-bottom);
			background: var(--color-sidebar);
			border-top: 1px solid var(--color-border);
		}
		.bottom-bar a,
		.bottom-bar button {
			display: flex;
			flex-direction: column;
			align-items: center;
			justify-content: center;
			gap: var(--space-1);
			font: inherit;
			font-size: var(--text-2xs);
			font-weight: 500;
			color: var(--color-text-muted);
			text-decoration: none;
			background: none;
			border: 0;
		}
		.bottom-bar [aria-current='page'] {
			font-weight: 600;
			color: var(--color-link);
		}
	}
</style>
