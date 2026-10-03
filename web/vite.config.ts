import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { defineConfig } from 'vite';

// The Go server (`vitamux serve`) serves the built SPA; during development Vite
// proxies API and health routes to it.
const backend = process.env.VITAMUX_DEV_BACKEND ?? 'http://127.0.0.1:8080';

export default defineConfig({
	plugins: [
		sveltekit({
			// SPA: one fallback shell, no SSR (see docs/adr/0010-sveltekit-static-spa.md).
			adapter: adapter({ pages: 'build', assets: 'build', fallback: 'index.html', strict: true }),
			csp: {
				mode: 'hash',
				directives: {
					'default-src': ['self'],
					'script-src': ['self'],
					'style-src': ['self'],
					'img-src': ['self', 'data:'],
					'connect-src': ['self'],
					'object-src': ['none'],
					'base-uri': ['self'],
					'form-action': ['self']
				}
			}
		})
	],
	server: {
		host: '127.0.0.1',
		proxy: {
			'/api': backend,
			'/healthz': backend,
			'/readyz': backend
		}
	}
});
