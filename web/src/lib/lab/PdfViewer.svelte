<!--
	One PDF page on a canvas, with the selected row's area outlined (bbox in page fractions,
	origin top left). pdf.js is imported when the viewer mounts, so it stays out of the initial
	bundle; it runs in its own worker without WebAssembly or eval, within the strict CSP.
	The outline is an SVG over the canvas: positions are attributes, never inline styles.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import type { PDFDocumentLoadingTask, PDFDocumentProxy, RenderTask } from 'pdfjs-dist';

	type BBox = { x0: number; y0: number; x1: number; y1: number };

	let {
		data,
		page = $bindable(1),
		bbox = null
	}: { data: ArrayBuffer; page?: number; bbox?: BBox | null } = $props();

	let pdf = $state<PDFDocumentProxy | null>(null);
	let failed = $state('');
	let width = $state(0);
	let canvas = $state<HTMLCanvasElement>();
	let scroller = $state<HTMLDivElement>();
	let task: RenderTask | null = null;

	const pages = $derived(pdf?.numPages ?? 0);

	onMount(() => {
		let destroyed = false;
		let loading: PDFDocumentLoadingTask | null = null;
		(async () => {
			try {
				const [pdfjs, worker] = await Promise.all([import('pdfjs-dist'), import('pdfjs-dist/build/pdf.worker.min.mjs?url')]);
				if (destroyed) return;
				pdfjs.GlobalWorkerOptions.workerSrc = worker.default;
				loading = pdfjs.getDocument({ data: new Uint8Array(data.slice(0)), useWasm: false });
				pdf = await loading.promise;
			} catch {
				if (!destroyed) failed = 'The PDF could not be displayed. The rows can still be reviewed.';
			}
		})();
		return () => {
			destroyed = true;
			task?.cancel();
			void loading?.destroy();
		};
	});

	async function render(doc: PDFDocumentProxy, n: number, cssWidth: number) {
		task?.cancel();
		if (!canvas) return;
		const p = await doc.getPage(n);
		const base = p.getViewport({ scale: 1 });
		const viewport = p.getViewport({ scale: cssWidth / base.width });
		const dpr = window.devicePixelRatio || 1;
		canvas.width = Math.floor(viewport.width * dpr);
		canvas.height = Math.floor(viewport.height * dpr);
		task = p.render({ canvas, viewport, transform: dpr === 1 ? undefined : [dpr, 0, 0, dpr, 0, 0] });
		try {
			await task.promise;
		} catch {
			// cancelled by a newer render
		}
	}

	$effect(() => {
		if (pdf && width > 0 && page >= 1 && page <= pdf.numPages) void render(pdf, page, width);
	});

	// Bring the outlined row into view.
	$effect(() => {
		if (!bbox || !pdf || width === 0) return;
		const top = bbox.y0;
		void tick().then(() => {
			if (scroller && canvas) scroller.scrollTop = Math.max(0, top * canvas.clientHeight - 48);
		});
	});
</script>

<div class="viewer">
	{#if failed}
		<p class="muted" role="status">{failed}</p>
	{:else}
		<div class="bar">
			<button class="btn sm" type="button" disabled={page <= 1} onclick={() => (page -= 1)}>Previous page</button>
			<span aria-live="polite">{pages ? `Page ${page} of ${pages}` : 'Loading PDF…'}</span>
			<button class="btn sm" type="button" disabled={!pages || page >= pages} onclick={() => (page += 1)}>Next page</button>
		</div>
		<div class="scroller" bind:this={scroller}>
			<div class="frame" bind:clientWidth={width} role="img" aria-label="Page {page} of the PDF{bbox ? ', selected row outlined' : ''}">
				<canvas bind:this={canvas} data-page={page}></canvas>
				{#if bbox && pages}
					<svg class="overlay" viewBox="0 0 1 1" preserveAspectRatio="none" aria-hidden="true">
						<rect class="highlight" x={bbox.x0} y={bbox.y0} width={bbox.x1 - bbox.x0} height={bbox.y1 - bbox.y0} vector-effect="non-scaling-stroke" />
					</svg>
				{/if}
			</div>
		</div>
	{/if}
</div>

<style>
	.viewer {
		display: grid;
		gap: var(--space-2);
	}
	.bar {
		display: flex;
		gap: var(--space-3);
		align-items: center;
		justify-content: space-between;
	}
	.scroller {
		max-height: 75vh;
		overflow: auto;
		background: var(--color-surface-2);
		border: 1px solid var(--color-border);
		border-radius: var(--radius-sm);
	}
	.frame {
		position: relative;
	}
	canvas {
		display: block;
		width: 100%;
		height: auto;
		background: #fff;
	}
	.overlay {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		pointer-events: none;
	}
	.highlight {
		fill: var(--color-focus);
		fill-opacity: 0.15;
		stroke: var(--color-focus);
		stroke-width: 2;
	}
</style>
