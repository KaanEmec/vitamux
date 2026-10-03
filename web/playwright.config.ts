import { defineConfig, devices } from '@playwright/test';

// Two projects, never run together:
//  chromium (default): E2E against the built SPA (vite preview of web/build, with its real CSP).
//    The API is stubbed per test with page.route (e2e/fake-api.ts); no Go server or database.
//    Run: npm run test:e2e (builds first). CI builds once, then runs `npx playwright test`.
//  stack: one smoke spec (e2e-stack) against a real `vitamux serve` on a throwaway database.
//    Run: make test-e2e-stack (scripts/e2e-stack.sh sets VITAMUX_E2E_STACK_URL and the sign-in).
const stackURL = process.env.VITAMUX_E2E_STACK_URL;
const port = 4173;

export default defineConfig({
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	reporter: process.env.CI ? 'github' : 'list',
	use: { trace: 'retain-on-failure' },
	...(stackURL
		? {
				projects: [
					{ name: 'stack', testDir: 'e2e-stack', use: { ...devices['Desktop Chrome'], baseURL: stackURL } }
				]
			}
		: {
				projects: [
					{
						name: 'chromium',
						testDir: 'e2e',
						use: { ...devices['Desktop Chrome'], baseURL: `http://127.0.0.1:${port}` }
					}
				],
				webServer: {
					command: `npx vite preview --host 127.0.0.1 --port ${port} --strictPort`,
					url: `http://127.0.0.1:${port}/login`,
					reuseExistingServer: !process.env.CI
				}
			})
});
