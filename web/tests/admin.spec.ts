/// <reference types="node" />
// The admin frame, the Recorders page and the Settings page (issue #111;
// SPEC.md R10, R13, R14, R84, R88, A36; ADR 0027), signed in from the
// radio's root URL through the made-up Authentik: the Admin menu for an
// admin only, the pages refusing anyone else as the admin API does, the
// recorders' state with two recorders' overlapping calls on hidden 1003
// (scripts/ci/e2e-web.sh's Ridge), Settings asking before it shortens
// retention, the disk warning on admin pages only, and axe in both looks
// and both themes at 375 px and 1,280 px. The tests that change what other
// tests see (retention, a recorder's connection) are admin-live.spec.ts's.
import { test, expect, type Page } from '@playwright/test';
import { axe, looks, recorderCard, saveLook, shot, signIn, themes, widths } from './admin.ts';

const side = (page: Page) => page.locator('nav.side');
const adminPaths = ['/admin/recorders', '/admin/audit', '/admin/settings'];

test('signed out: no Admin menu, the admin pages offer sign-in, and the admin API refuses', async ({ page, request }) => {
	await page.goto('/');
	await expect(side(page).getByRole('link', { name: 'Listen' })).toBeVisible();
	await expect(side(page).getByText('Admin', { exact: true })).toHaveCount(0);
	for (const path of adminPaths) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('This page is for admins');
		await expect(page.locator('main').getByRole('link', { name: 'Sign in' })).toHaveAttribute('href', `/auth/signin?return=${encodeURIComponent(path)}`);
		await expect(page.locator('main')).not.toContainText('Bench');
	}
	for (const path of ['/recorders', '/rejections', '/overlaps', '/retention', '/audit', '/audit/retention']) expect((await request.get(`/api/v1/admin${path}`)).status(), path).toBe(401);
});

test('a listener: no Admin menu, "This page is for admins" and nothing else, and the admin API refuses', async ({ page }) => {
	await signIn(page, 'listener');
	await expect(side(page).getByText('Admin', { exact: true })).toHaveCount(0);
	await page.setViewportSize({ width: 375, height: 800 });
	await page.getByRole('button', { name: 'More' }).click();
	await expect(page.getByRole('dialog', { name: 'More' }).getByRole('link')).toHaveCount(0);
	await page.keyboard.press('Escape');
	await page.setViewportSize({ width: 1280, height: 800 });
	for (const path of adminPaths) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toHaveText('This page is for admins');
		await expect(page.locator('main')).toHaveText('This page is for admins');
	}
	for (const path of ['/recorders', '/rejections', '/overlaps', '/retention', '/audit', '/audit/retention']) expect((await page.request.get(`/api/v1/admin${path}`)).status(), path).toBe(403);
});

test('an admin: the Admin menu on desktops, and under More on phones; its pages open from it', async ({ page }) => {
	await signIn(page, 'admin');
	const group = side(page).getByRole('link', { name: 'Admin', exact: true });
	await expect(group).toHaveAttribute('href', '/admin/recorders');
	await expect(side(page).getByRole('button', { name: 'Admin: hide its pages' })).toHaveAttribute('aria-expanded', 'true');
	await side(page).getByRole('link', { name: 'Recorders' }).click();
	await expect(page).toHaveURL(/\/admin\/recorders$/);
	await expect(page).toHaveTitle('Recorders · Admin · RadioArchive');
	await expect(side(page).getByRole('link', { name: 'Recorders' })).toHaveAttribute('aria-current', 'page');
	await side(page).getByRole('link', { name: 'Settings' }).click();
	await expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible();
	// The listener's pages are still there, with the dock.
	await expect(side(page).getByRole('link', { name: 'Listen' })).toBeVisible();
	await expect(page.getByRole('region', { name: 'Now playing' })).toBeVisible();

	await page.setViewportSize({ width: 375, height: 800 });
	await page.getByRole('navigation', { name: 'Main' }).getByRole('button', { name: 'More' }).click();
	const more = page.getByRole('dialog', { name: 'More' });
	await expect(more.getByRole('link', { name: 'Admin', exact: true })).toBeVisible();
	await more.getByRole('link', { name: 'Recorders' }).click();
	await expect(page.getByRole('heading', { level: 1, name: 'Recorders' })).toBeVisible();
});

test("the Recorders page: each recorder's state, the overlap on 1003, the quiet threshold's form, and no keys (R10, R13, R14)", async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/recorders');
	const bench = recorderCard(page, 'Bench');
	await expect(bench).toContainText(/Connected since /);
	await expect(bench).toContainText(/Last connected\s*[A-Z][a-z]{2},? \d+ [A-Z][a-z]{2},? \d\d:\d\d \((just now|\d+ (min|h) ago)\)/);
	await expect(bench).toContainText('Client version');
	await expect(bench.locator('.systems li')).toHaveText(/ex\s*→ EX\s*p25\s*Decode rate: 40\.0 messages a second/);
	// Ridge has never connected; it recorded 1003's three calls as Bench did.
	const ridge = recorderCard(page, 'Ridge');
	await expect(ridge).toContainText('Never connected');
	await expect(ridge.getByText('OK', { exact: true })).toBeVisible();
	await expect(ridge.getByText('Warning', { exact: true })).toHaveCount(0);
	await expect(page.getByRole('list', { name: 'Overlapping calls' })).toHaveText('Bench and Ridge both recorded TG 1003: 3 overlapping calls in the last 24 h');
	// Keys are the shell's alone (R10).
	expect(await page.locator('main').innerText()).not.toMatch(/\bkeys?\b|hash|rotat/i);
	const api = await (await page.request.get('/api/v1/admin/recorders')).text();
	expect(api).not.toMatch(/key|hash|rak_/i);

	// The quiet threshold (ADR 0010): whole minutes, a problem under the field, then off again.
	const field = ridge.getByRole('textbox', { name: 'Quiet after (minutes)' });
	await expect(field).toHaveValue('');
	await field.fill('ten');
	await ridge.getByRole('button', { name: 'Save the quiet signal' }).click();
	await expect(field).toHaveAttribute('aria-invalid', 'true');
	await expect(field).toBeFocused();
	await expect(ridge.locator('.field-error')).toHaveText('Enter whole minutes from 1 to 10,080, or leave it empty for off.');
	await field.fill('45');
	await ridge.getByRole('button', { name: 'Save the quiet signal' }).click();
	await expect(ridge.getByRole('status')).toHaveText('Saved: quiet after 45 minutes with no call.');
	await expect(field).not.toHaveAttribute('aria-invalid', /./);
	await page.reload();
	await expect(recorderCard(page, 'Ridge').getByRole('textbox', { name: 'Quiet after (minutes)' })).toHaveValue('45');
	await recorderCard(page, 'Ridge').getByRole('textbox', { name: 'Quiet after (minutes)' }).fill('');
	await recorderCard(page, 'Ridge').getByRole('button', { name: 'Save the quiet signal' }).click();
	await expect(recorderCard(page, 'Ridge').getByRole('status')).toHaveText('Saved: the quiet signal is off.');
});

test("Settings: shortening retention names how many recordings go and asks; cancelling changes nothing", async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/settings');
	const days = page.getByRole('textbox', { name: 'Retention period (days) required' });
	await expect(days).toHaveValue('30');
	await expect(days).toHaveAttribute('required', '');
	// A problem under the field, focus on it.
	await days.fill('0');
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(days).toHaveAttribute('aria-invalid', 'true');
	await expect(days).toBeFocused();
	await expect(page.locator('#days-error')).toHaveText(/^Enter whole days from 1 to [\d,]+\.$/);
	// Shorter: the server's count, named, and nothing saved until confirmed.
	const ask = await page.request.put('/api/v1/admin/retention', { data: { days: 1 } });
	expect(ask.status()).toBe(409);
	const n = ((await ask.json()) as { recordings: number }).recordings;
	expect(n).toBeGreaterThan(0);
	await days.fill('1');
	await page.getByRole('button', { name: 'Save', exact: true }).click();
	const confirm = page.getByRole('region', { name: `Delete ${n} recording${n === 1 ? '' : 's'}?` });
	await expect(confirm).toBeVisible();
	await expect(confirm.getByRole('heading')).toBeFocused();
	await expect(confirm).toContainText(`Shortening retention from 30 days to 1 day deletes ${n} recording${n === 1 ? '' : 's'}`);
	await confirm.getByRole('button', { name: 'Cancel' }).click();
	await expect(confirm).toHaveCount(0);
	await expect(page.locator('p.status')).toHaveText('Nothing changed: recordings are still kept for 30 days.');
	await expect(days).toHaveValue('30');
	expect(((await (await page.request.get('/api/v1/admin/retention')).json()) as { days: number }).days).toBe(30);
});

/** The admin API's retention answer, with the recordings volume at 12 % free. */
async function lowDisk(page: Page) {
	await page.route('**/api/v1/admin/retention', async (route) => {
		if (route.request().method() !== 'GET') return route.continue();
		const r = await route.fetch();
		const body = (await r.json()) as { disk: Record<string, unknown> };
		body.disk = {
			...body.disk,
			free_percent: 12,
			state: 'warning',
			message: 'The recordings volume has 12 % free. Below 5 %, the oldest recordings are deleted early.'
		};
		await route.fulfill({ response: r, json: body });
	});
}
const DISK = 'The recordings volume has 12 % free. Below 5 %, the oldest recordings are deleted early.';

test('the disk warning: on every admin page below 15 % free, and on no listener page (R88)', async ({ page }) => {
	await signIn(page, 'admin');
	await lowDisk(page);
	for (const path of adminPaths) {
		await page.goto(path);
		await expect(page.getByRole('main').getByText(DISK, { exact: true })).toBeVisible();
	}
	for (const path of ['/', '/recent', '/basket']) {
		await page.goto(path);
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		await expect(page.getByText(DISK)).toHaveCount(0);
	}
	// At the real volume's state (well above 15 % here), none.
	await page.unrouteAll();
	await page.goto('/admin/recorders');
	await expect(page.getByRole('heading', { level: 1, name: 'Recorders' })).toBeVisible();
	await expect(page.getByRole('main').getByText(/^The recordings volume has/)).toHaveCount(0);
});

test("the admin pages say so when the server can't be reached, as an alert", async ({ page }) => {
	await signIn(page, 'admin');
	await page.route('**/api/v1/admin/recorders', (route) => route.abort());
	await page.goto('/admin/recorders');
	await expect(page.getByRole('heading', { level: 1, name: 'Recorders' })).toBeVisible();
	await expect(page.getByRole('main').getByRole('alert')).toHaveText("Couldn't reach the server just now. Try again in a minute.");
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the admin menu, the Recorders page and Settings with its confirmation, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(90_000);
				await saveLook(context, baseURL!, look, theme);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				await lowDisk(page);
				const tag = `${look}-${theme}-${width}`;

				// The menu: the side menu on desktops, More on phones.
				await page.goto('/admin/recorders');
				await expect(recorderCard(page, 'Bench')).toBeVisible();
				await expect(page.getByRole('list', { name: 'Overlapping calls' })).toBeVisible();
				await expect(page.getByRole('main').getByText(DISK, { exact: true })).toBeVisible();
				await axe(page, width);
				await shot(page, `recorders-${tag}`);
				if (width === 375) {
					await page.getByRole('navigation', { name: 'Main' }).getByRole('button', { name: 'More' }).click();
					await expect(page.getByRole('dialog', { name: 'More' }).getByRole('link', { name: 'Settings' })).toBeVisible();
					await axe(page, width);
					await shot(page, `menu-${tag}`);
					await page.keyboard.press('Escape');
				} else {
					await shot(page, `menu-${tag}`);
				}

				// A problem under the quiet field.
				const ridge = recorderCard(page, 'Ridge');
				await ridge.getByRole('textbox', { name: 'Quiet after (minutes)' }).fill('0');
				await ridge.getByRole('button', { name: 'Save the quiet signal' }).click();
				await expect(ridge.locator('.field-error')).toBeVisible();
				await axe(page, width);

				// Settings, asking before it shortens retention; cancelled.
				await page.goto('/admin/settings');
				await page.getByRole('textbox', { name: /Retention period/ }).fill('1');
				await page.getByRole('button', { name: 'Save', exact: true }).click();
				const confirm = page.getByRole('region', { name: /^Delete \d+ recordings?\?$/ });
				await expect(confirm).toBeVisible();
				await axe(page, width);
				await shot(page, `settings-${tag}`);
				await confirm.getByRole('button', { name: 'Cancel' }).click();
				await page.getByRole('textbox', { name: /Retention period/ }).fill('');
				await page.getByRole('button', { name: 'Save', exact: true }).click();
				await expect(page.locator('#days-error')).toBeVisible();
				await axe(page, width);

				// The page a listener or a signed-out visitor gets.
				await page.context().clearCookies();
				await saveLook(context, baseURL!, look, theme);
				await page.unrouteAll();
				await page.goto('/admin/settings');
				await expect(page.getByRole('heading', { level: 1, name: 'This page is for admins' })).toBeVisible();
				await axe(page, width);
			});
