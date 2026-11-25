// The backup's alert on the admin pages (issue #150; SPEC.md R89, A36; ADR
// 0041): when the last backup failed, or the last good one is more than 36
// hours old, every admin page shows why, until a good backup clears it; no
// listener page ever does. The binary reads a good status file here
// (e2e-web.sh), so the failing state is the admin API's answer, changed in
// the browser, as the disk warning's is.
import { expect, test, type Page } from '@playwright/test';
import { axe, inLook, looks, ownLook, saveLook, shot, signIn, themes, widths } from './admin.ts';

const adminPaths = ['/admin/talkgroups', '/admin/units', '/admin/systems', '/admin/recorders', '/admin/accounts', '/admin/audit', '/admin/settings'];

const WHY = 'The last backup failed: the dump of radioarchive failed. The last good one was the night of 2026-11-24.';

/** The admin API says the backups are failing, in the server's words. */
async function failing(page: Page) {
	await page.route('**/api/v1/admin/backup', async (route) => {
		const r = await route.fetch();
		const body = (await r.json()) as Record<string, unknown>;
		await route.fulfill({
			response: r,
			json: { ...body, state: 'failing', last_run_ok: false, last_good_night: '2026-11-24', message: WHY }
		});
	});
}

const alertOf = (page: Page) => page.getByRole('main').getByText('Backups are failing.');

test('a good backup: no alert, and the admin API answers its state (R89)', async ({ page }) => {
	await signIn(page, 'admin');
	const r = await page.request.get('/api/v1/admin/backup');
	expect(r.status()).toBe(200);
	expect(((await r.json()) as { state: string }).state).toBe('ok');
	await page.goto('/admin/settings');
	await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
	await expect(alertOf(page)).toHaveCount(0);
});

test('failing: the alert says why on every admin page, and on no listener page (R89)', async ({ page }) => {
	await signIn(page, 'admin');
	await failing(page);
	for (const path of adminPaths) {
		await page.goto(path);
		await expect(alertOf(page)).toBeVisible();
		await expect(page.getByRole('main').getByText(WHY, { exact: false })).toBeVisible();
	}
	for (const path of ['/', '/recent', '/basket']) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await expect(page.getByText('Backups are failing.')).toHaveCount(0);
	}
	// A good backup clears it at the next look.
	await page.unrouteAll();
	await page.goto('/admin/recorders');
	await expect(page.getByRole('heading', { level: 1, name: 'Recorders' })).toBeVisible();
	await expect(alertOf(page)).toHaveCount(0);
});

test('signed out and as a listener, the admin API keeps the backup to itself', async ({ page, request }) => {
	expect((await request.get('/api/v1/admin/backup')).status()).toBe(401);
	await signIn(page, 'listener');
	expect((await page.request.get('/api/v1/admin/backup')).status()).toBe(403);
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the backup alert, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				await saveLook(context, baseURL!, look, theme);
				await ownLook(page);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				await failing(page);
				await page.goto('/admin/settings');
				await expect(alertOf(page)).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `backup-failing-${look}-${theme}-${width}`);
			});
