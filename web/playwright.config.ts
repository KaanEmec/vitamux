import { defineConfig, devices } from '@playwright/test';

// E2E against the built SPA (vite preview of web/build, with its real CSP). The API is
// stubbed per test with page.route (e2e/fake-api.ts); no Go server or database needed.
// Run: npm run test:e2e (builds first). CI builds once, then runs `npx playwright test`.
const port = 4173;

export default defineConfig({
	testDir: 'e2e',
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	reporter: process.env.CI ? 'github' : 'list',
	use: { baseURL: `http://127.0.0.1:${port}`, trace: 'retain-on-failure' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
	webServer: {
		command: `npx vite preview --host 127.0.0.1 --port ${port} --strictPort`,
		url: `http://127.0.0.1:${port}/login`,
		reuseExistingServer: !process.env.CI
	}
});
