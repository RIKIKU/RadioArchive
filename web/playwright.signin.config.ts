/// <reference types="node" />
import { defineConfig, devices } from '@playwright/test';

// Listeners' sign-in (A33) against the compose stack with Authentik and
// Mailpit: scripts/ci/e2e-signin.sh starts it and sets RA_URL,
// RA_AUTHENTIK_URL and RA_MAILPIT_URL. The other browser tests
// (playwright.config.ts) run against the binary alone, with sign-in off.
const baseURL = process.env.RA_URL ?? 'http://localhost:8080';

export default defineConfig({
	testDir: 'signin-tests',
	// The tests share one Authentik, and each makes its own accounts, so they
	// may run at once; sign-up codes are found by address.
	fullyParallel: true,
	// Authentik is the slow part; two at once keep a small runner steady.
	workers: process.env.CI ? 2 : undefined,
	retries: process.env.CI ? 1 : 0,
	globalTimeout: process.env.CI ? 15 * 60_000 : 0,
	timeout: 120_000,
	reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
	use: { baseURL, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
	// Authentik's own pages are the same in every browser; Chromium is
	// enough for what the radio adds to them.
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
});
