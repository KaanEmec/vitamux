<!--
	A QR code as an inline SVG path (no {@html}, no inline style, so the CSP holds). Black on white
	with a four-module quiet zone whatever the theme, because scanners need the contrast.
-->
<script lang="ts">
	import qrcode from 'qrcode-generator';

	let { value, label }: { value: string; label: string } = $props();

	const quiet = 4;
	const qr = $derived.by(() => {
		const code = qrcode(0, 'M');
		code.addData(value);
		code.make();
		const n = code.getModuleCount();
		let path = '';
		for (let row = 0; row < n; row++) {
			for (let col = 0; col < n; col++) if (code.isDark(row, col)) path += `M${col + quiet} ${row + quiet}h1v1h-1z`;
		}
		return { size: n + 2 * quiet, path };
	});
</script>

<svg class="qr" viewBox="0 0 {qr.size} {qr.size}" role="img" aria-label={label} shape-rendering="crispEdges">
	<rect width={qr.size} height={qr.size} fill="#fff" />
	<path d={qr.path} fill="#000" />
</svg>

<style>
	.qr {
		display: block;
		width: min(14rem, 100%);
		height: auto;
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
</style>
