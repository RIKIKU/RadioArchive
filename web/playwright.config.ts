/// <reference types="node" />
import { defineConfig, devices } from '@playwright/test';

// scripts/ci/e2e-web.sh builds the binary with the app in it, starts it on
// a fresh database and sets RA_URL.
const baseURL = process.env.RA_URL ?? 'http://127.0.0.1:18090';

export default defineConfig({
	testDir: 'tests',
	fullyParallel: true,
	// In CI a failed test is tried once more, and reported flaky if it then
	// passes. By hand, no retries.
	retries: process.env.CI ? 1 : 0,
	// A stuck run stops, with its report, before the job's own time limit.
	globalTimeout: process.env.CI ? 15 * 60_000 : 0,
	reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
	use: { baseURL, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
	// A7: Chrome (Chromium), Firefox and WebKit. WebKit stays though iOS
	// isn't supported (R40): it's Safari on other platforms.
	projects: [
		{ name: 'chromium', use: { ...devices['Desktop Chrome'] } },
		{ name: 'firefox', use: { ...devices['Desktop Firefox'] } },
		{ name: 'webkit', use: { ...devices['Desktop Safari'] } }
	]
});
