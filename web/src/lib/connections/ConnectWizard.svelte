<!-- The Connect a source dialog: the guided setup of lib/setup/SourceSetup.svelte. -->
<script lang="ts">
	import Modal from '../components/Modal.svelte';
	import SourceSetup from '../setup/SourceSetup.svelte';

	let { start, onclose }: { start?: { provider: string; app?: boolean }; onclose: () => void } = $props();
</script>

<Modal title="Connect a source" {onclose}>
	<SourceSetup {start} />
</Modal>
