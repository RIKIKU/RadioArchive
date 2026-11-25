/// <reference types="node" />
import { defineConfig, devices } from '@playwright/test';

// The PowerShell module's behaviour tests (issue #116, ADR 0028) against
// the compose stack with Authentik and Mailpit: scripts/ci/e2e-module.sh
// starts them, after the sign-in tests, with RA_PWSH (the pwsh to run) and
// RA_PS_MODULES (SecretManagement and SecretStore) set. Each PowerShell
// "window" is its own pwsh process; Playwright's browser plays the person
// signing in through Authentik's own pages.
const baseURL = process.env.RA_URL ?? 'http://localhost:8080';

export default defineConfig({
	testDir: 'module-tests',
	// One after another: the tests share the admins they make.
	fullyParallel: false,
	workers: 1,
	retries: process.env.CI ? 1 : 0,
	globalTimeout: process.env.CI ? 15 * 60_000 : 0,
	timeout: 180_000,
	reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
	use: { baseURL, trace: 'retain-on-failure', screenshot: 'only-on-failure' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }]
});
