// The automatic upgrade's alert on the admin pages (issue #153; SPEC.md
// R87, R89, A36; ADR 0044): when an upgrade failed, every admin page says
// so, until a later upgrade or clear; no listener page ever does. The
// binary has no upgrade status here, so the failing state is the admin
// API's answer, changed in the browser, as the disk warning's is.
import { expect, test, type Page } from '@playwright/test';
import { axe, inLook, looks, ownLook, saveLook, shot, signIn, themes, widths } from './admin.ts';

const adminPaths = ['/admin/talkgroups', '/admin/units', '/admin/systems', '/admin/recorders', '/admin/accounts', '/admin/audit', '/admin/settings'];

const WHY =
	"The automatic upgrade from 0.1.1 to 0.1.2 failed (the API didn't answer as 0.1.2 within 600 s (it runs 0.1.1)) and was rolled back. 0.1.2 won't be tried again until you run deploy/auto-upgrade/auto-upgrade.sh retry 0.1.2.";

async function failing(page: Page) {
	await page.route('**/api/v1/admin/upgrade', (route) =>
		route.fulfill({
			json: { state: 'failing', last_check_at: new Date().toISOString(), last_result: 'rolled-back', running_version: '0.1.1', latest_version: '0.1.2', last_upgrade_at: null, message: WHY }
		})
	);
}

const alertOf = (page: Page) => page.getByRole('main').getByText('The automatic upgrade failed.');

test('failing: the alert says what failed on every admin page, and on no listener page (R89)', async ({ page }) => {
	await signIn(page, 'admin');
	await failing(page);
	for (const path of adminPaths) {
		await page.goto(path);
		await expect(alertOf(page)).toBeVisible();
	}
	for (const path of ['/', '/recent', '/basket']) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await expect(page.getByText('The automatic upgrade failed.')).toHaveCount(0);
	}
	// Off (the binary's own answer here): none.
	await page.unrouteAll();
	await page.goto('/admin/recorders');
	await expect(page.getByRole('heading', { level: 1, name: 'Recorders' })).toBeVisible();
	await expect(alertOf(page)).toHaveCount(0);
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the upgrade alert, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				await saveLook(context, baseURL!, look, theme);
				await ownLook(page);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				await failing(page);
				await page.goto('/admin/settings');
				await expect(alertOf(page)).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `upgrade-failing-${look}-${theme}-${width}`);
			});
