/// <reference types="node" />
import { defineConfig, devices } from '@playwright/test';

// SPEC.md section 9's steps 2 and 3 (issue #155), against an install made
// from the release's files, behind the deploy guide's nginx example:
// scripts/ci/e2e-section9.sh starts it and sets RA_URL and the rest.
export default defineConfig({
	testDir: 'section9-tests',
	workers: 1,
	retries: 0,
	timeout: 300_000,
	reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
	use: { baseURL: process.env.RA_URL, trace: 'retain-on-failure', screenshot: 'only-on-failure', acceptDownloads: true },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'], launchOptions: { args: ['--autoplay-policy=no-user-gesture-required'] } } }]
});
