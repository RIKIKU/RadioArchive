/// <reference types="node" />
// Beside PagerArchive (SPEC.md R102, R104, A34; ADR 0056): the app switcher
// in the top bar, and the look cookie the two apps share on their parent
// domain; and standalone, neither.
//
// The @combined tests run against a binary of their own that e2e-web.sh
// starts with PAGERARCHIVE_URL=http://pager.example.test and
// LOOK_COOKIE_DOMAIN=example.test. A cookie needs a domain name, not an
// address, so the browser reaches it as http://radio.example.test through
// it as the browser's HTTP proxy (RA_COMBINED_PROXY): Go's server takes a
// proxy's absolute request and reads its host as the page's. Any host goes
// to it, so pager.example.test is the radio too, which is all the
// switcher's click needs. The live feed is the test's own
// (page.routeWebSocket), so no socket goes through the proxy.
import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import { axe, looks, shot, themes, widths } from './admin.ts';
import { accountLook, accountTheme } from '../src/lib/look.ts';

const RADIO = 'http://radio.example.test';
const PAGER = 'http://pager.example.test';
const YEAR = 365 * 24 * 60 * 60;

const switcher = (page: Page) => page.getByRole('link', { name: 'Switch to Pager' });

/** The live feed, quiet: it only welcomes. */
async function quietFeed(page: Page) {
	await page.routeWebSocket(/\/api\/v1\/live$/, (ws) => {
		ws.send(JSON.stringify({ type: 'welcome', protocol: 1, last_event_id: 0 }));
		ws.onMessage((m) => {
			if (JSON.parse(String(m)).type === 'set') ws.send(JSON.stringify({ type: 'listening' }));
		});
	});
}

async function chooseLook(page: Page, look: 'harbour' | 'console') {
	await page.getByRole('button', { name: /^Account menu/ }).click();
	await page.getByLabel('Look').selectOption(look);
	await page.keyboard.press('Escape');
}

async function chooseTheme(page: Page, theme: 'system' | 'light' | 'dark') {
	await page.getByRole('button', { name: /^Account menu/ }).click();
	await page.getByLabel('Colour theme').selectOption(theme);
	await page.keyboard.press('Escape');
}

const lookCookies = async (context: BrowserContext) => (await context.cookies()).filter((c) => c.name === 'look');
const themeCookies = async (context: BrowserContext) => (await context.cookies()).filter((c) => c.name === 'theme');
const where = (cs: { domain: string; value: string }[]) => cs.map((c) => `${c.domain} ${c.value}`);

// The look a signed-in account comes in with (R104): shared, the cookie's
// when it holds a known look, else the account's; standalone, the account's.
test('a signed-in account’s look: the shared cookie’s wins', () => {
	test.skip(test.info().project.name !== 'chromium', 'no browser: once is enough');
	expect(accountLook('harbour', true, 'look=console')).toBe('console');
	expect(accountLook('console', true, 'theme=dark; look=harbour')).toBe('harbour');
	expect(accountLook('console', true, '')).toBe('console');
	expect(accountLook('console', true, 'look=bogus')).toBe('console');
	expect(accountLook(undefined, true, 'mylook=console')).toBe('harbour');
	expect(accountLook('harbour', false, 'look=console')).toBe('harbour');
	expect(accountLook(undefined, false, '')).toBe('harbour');
	// The theme (ArchiveUx 0.7.0): an empty or unknown cookie is passed over.
	expect(accountTheme('light', true, 'theme=dark')).toBe('dark');
	expect(accountTheme('dark', true, 'theme=; theme=light')).toBe('light');
	expect(accountTheme('dark', true, 'theme=system')).toBe('dark');
	expect(accountTheme(undefined, true, '')).toBe('system');
	expect(accountTheme('light', false, 'theme=dark')).toBe('light');
});

test.describe('standalone', () => {
	for (const width of widths)
		test(`no app switcher, ${width}px`, async ({ page }) => {
			await page.setViewportSize({ width, height: 800 });
			await quietFeed(page);
			const res = await page.goto('/');
			expect(await res!.text()).not.toMatch(/data-look-shared|data-pagerarchive/);
			await expect(page.getByRole('button', { name: /^Account menu/ })).toBeVisible({ timeout: 15_000 });
			await expect(page.locator('[data-app-switcher]')).toHaveCount(0);
			await expect(page.getByRole('link', { name: /^Switch to / })).toHaveCount(0);
		});

	test('the look cookie is this host’s own', async ({ page, context, baseURL }) => {
		await quietFeed(page);
		await page.goto('/');
		await chooseLook(page, 'console');
		await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
		const host = new URL(baseURL!).hostname;
		await expect.poll(() => lookCookies(context)).toEqual([expect.objectContaining({ value: 'console', domain: host, path: '/', httpOnly: false, sameSite: 'Lax' })]);
		// The next page is sent in it, with nothing shared.
		const res = await page.goto('/recent');
		expect(await res!.text()).toContain('<html data-look="console"');
	});

	test('the theme cookie is this host’s own', async ({ page, context, baseURL }) => {
		await quietFeed(page);
		await page.goto('/');
		await chooseTheme(page, 'dark');
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		const host = new URL(baseURL!).hostname;
		await expect.poll(() => themeCookies(context)).toEqual([expect.objectContaining({ value: 'dark', domain: host, path: '/', httpOnly: false, sameSite: 'Lax' })]);
		const res = await page.goto('/recent');
		expect(await res!.text()).toContain('<html data-theme="dark"');
		// Auto forgets it.
		await chooseTheme(page, 'system');
		await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
		await expect.poll(() => themeCookies(context)).toEqual([]);
	});
});

test.describe('beside PagerArchive @combined', () => {
	test.skip(!process.env.RA_COMBINED_PROXY, 'RA_COMBINED_PROXY names the combined binary: run through scripts/ci/e2e-web.sh');
	test.use({ baseURL: RADIO, proxy: { server: process.env.RA_COMBINED_PROXY ?? 'http://127.0.0.1:1' } });

	for (const look of looks)
		for (const theme of themes)
			for (const width of widths)
				test(`the app switcher, ${look}, ${theme}, ${width}px`, async ({ page, context }) => {
					// axe takes a while, the more so with the other browsers' runs beside it.
					test.setTimeout(90_000);
					await context.addCookies([
						{ name: 'look', value: look, domain: '.example.test', path: '/' },
						{ name: 'theme', value: theme, url: RADIO }
					]);
					await page.setViewportSize({ width, height: 800 });
					await quietFeed(page);
					await page.goto('/');
					const sw = switcher(page);
					await expect(sw).toBeVisible({ timeout: 15_000 });
					await expect(sw).toHaveText('Pager');
					await expect(sw).toHaveAttribute('href', PAGER);
					await expect(sw).not.toHaveAttribute('target', /./);
					// In the top bar, before the account menu, on desktops and phones.
					const top = page.locator('header.top');
					await expect(top.getByRole('link', { name: 'Switch to Pager' })).toBeVisible();
					const [s, a] = await Promise.all([sw.boundingBox(), page.getByRole('button', { name: /^Account menu/ }).boundingBox()]);
					expect(s!.x + s!.width).toBeLessThanOrEqual(a!.x + 1);
					await axe(page, width);
					await shot(page, `switcher-${look}-${theme}-${width}`);
				});

	test('the switcher opens PagerArchive in the same tab', async ({ page, context }) => {
		await quietFeed(page);
		await page.goto('/recent');
		await switcher(page).click();
		await expect(page).toHaveURL(`${PAGER}/`);
		expect(context.pages()).toHaveLength(1);
	});

	test('the first paint is the shared cookie’s, and stays it', async ({ page, context }) => {
		await context.addCookies([{ name: 'look', value: 'console', domain: '.example.test', path: '/' }]);
		await quietFeed(page);
		const res = await page.goto('/');
		expect(await res!.text()).toContain('<html data-look-shared="example.test" data-look="console"');
		await expect(page.getByRole('button', { name: /^Account menu/ })).toBeVisible({ timeout: 15_000 });
		await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
		// The shared cookie is the only look cookie.
		expect(await lookCookies(context)).toEqual([expect.objectContaining({ value: 'console', domain: '.example.test', path: '/' })]);
	});

	test('choosing a look writes it to the shared cookie, as ArchiveUx’s lookCookie does', async ({ page, context }) => {
		await quietFeed(page);
		await page.goto('/');
		await expect(page.locator('html')).not.toHaveAttribute('data-look', /./);
		const before = Date.now() / 1000;
		await chooseLook(page, 'console');
		await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
		await expect.poll(() => lookCookies(context)).toEqual([
			expect.objectContaining({ value: 'console', domain: '.example.test', path: '/', httpOnly: false, sameSite: 'Lax', secure: false })
		]);
		const [c] = await lookCookies(context);
		expect(c.expires).toBeGreaterThan(before + YEAR - 120);
		expect(c.expires).toBeLessThan(before + YEAR + 120);
		// PagerArchive's pages on the same domain get it: its next page there
		// is in it.
		const res = await page.goto(`${PAGER}/`);
		expect(await res!.text()).toContain('data-look="console"');
		await chooseLook(page, 'harbour');
		await expect.poll(async () => (await lookCookies(context)).map((c) => `${c.domain} ${c.value}`)).toEqual(['.example.test harbour']);
	});

	test('a look chosen here alone moves into the shared cookie', async ({ page, context }) => {
		// The radio's own cookie from before LOOK_COOKIE_DOMAIN was set.
		await context.addCookies([{ name: 'look', value: 'console', url: RADIO }]);
		await quietFeed(page);
		const res = await page.goto('/');
		expect(await res!.text()).toContain('<html data-look-shared="example.test" data-look="console"');
		await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
		expect((await lookCookies(context)).map((c) => `${c.domain} ${c.value}`)).toEqual(['.example.test console']);
	});

	// The light or dark theme is shared the same way (ArchiveUx 0.7.0).
	test('the first paint is the shared theme cookie’s', async ({ page, context }) => {
		await context.addCookies([{ name: 'theme', value: 'dark', domain: '.example.test', path: '/' }]);
		await quietFeed(page);
		const res = await page.goto('/');
		expect(await res!.text()).toContain('<html data-look-shared="example.test" data-theme="dark"');
		await expect(page.getByRole('button', { name: /^Account menu/ })).toBeVisible({ timeout: 15_000 });
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		expect(where(await themeCookies(context))).toEqual(['.example.test dark']);
	});

	test('choosing a theme writes it to the shared cookie; Auto deletes it', async ({ page, context }) => {
		await quietFeed(page);
		await page.goto('/');
		await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
		const before = Date.now() / 1000;
		await chooseTheme(page, 'dark');
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
		await expect.poll(() => themeCookies(context)).toEqual([
			expect.objectContaining({ value: 'dark', domain: '.example.test', path: '/', httpOnly: false, sameSite: 'Lax', secure: false })
		]);
		const [c] = await themeCookies(context);
		expect(c.expires).toBeGreaterThan(before + YEAR - 120);
		expect(c.expires).toBeLessThan(before + YEAR + 120);
		// PagerArchive's host on the same domain is sent in it.
		const res = await page.goto(`${PAGER}/`);
		expect(await res!.text()).toContain('data-theme="dark"');
		await chooseTheme(page, 'system');
		await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
		await expect.poll(() => themeCookies(context)).toEqual([]);
	});

	test('a theme chosen here alone moves into the shared cookie', async ({ page, context }) => {
		await context.addCookies([{ name: 'theme', value: 'light', url: RADIO }]);
		await quietFeed(page);
		const res = await page.goto('/');
		expect(await res!.text()).toContain('<html data-look-shared="example.test" data-theme="light"');
		await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
		expect(where(await themeCookies(context))).toEqual(['.example.test light']);
	});
});
