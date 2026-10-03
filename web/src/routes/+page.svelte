<script lang="ts">
	let status = $state<'checking' | 'ok' | 'unreachable'>('checking');

	$effect(() => {
		fetch('/healthz')
			.then((r) => (status = r.ok ? 'ok' : 'unreachable'))
			.catch(() => (status = 'unreachable'));
	});
</script>

<main>
	<h1>Vitamux</h1>
	<p>Configurator placeholder. Server status: <strong>{status}</strong></p>
</main>
