/// <reference types="node" />
// A20 in the browser (issue #142; SPEC.md R70-R73, R78; ADRs 0036-0039),
// against a binary of its own at the default limits (@limits): the one the
// other tests use has them raised. scripts/ci/e2e-web.sh starts it with
// TRUSTED_PROXY=127.0.0.1, so each test chooses its own client by
// X-Forwarded-For and the minute-long counts of one test don't reach the
// next, and with zips sent at 1 KB/s (LIMIT_ZIP_SPEED=0.001), so the
// test's own zips are still being sent when the page asks for a third.
// Every count is the default. They run one at a time (--workers=1), since
// "the radio is full" is the whole radio's.
//
// The test's own sockets and requests come from Node, with the address
// they count as; the page's from the browser, with the same address where
// it matters. A page's WebSocket may not carry X-Forwarded-For in every
// browser, so the per-client socket test uses no forwarded address at all:
// everything there is 127.0.0.1.
import { test, expect, request as apiRequest, type Browser, type BrowserContext, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const looks = ['harbour', 'console'] as const;
const themes = ['light', 'dark'] as const;
const widths = [375, 1280] as const;

// A client of each test's own, apart for each browser: an IPv6 /64.
// It starts from the clock, since a retried test's new worker starts the
// count again, and a client of a minute ago may still be used up.
let made = Math.floor(Date.now() / 1000) % 9000;
const client = () => `2001:db8:${(test.info().project.name.charCodeAt(0) % 64) + 1}:${++made}::1`;

/** A browser context whose requests count as from, in a look and theme. */
async function contextAs(browser: Browser, baseURL: string, from: string | undefined, look = 'harbour', theme = 'light'): Promise<BrowserContext> {
	const context = await browser.newContext(from ? { extraHTTPHeaders: { 'X-Forwarded-For': from } } : {});
	const url = new URL(baseURL).origin;
	await context.addCookies([
		{ name: 'look', value: look, url },
		{ name: 'theme', value: theme, url }
	]);
	return context;
}

/** Node's requests as from. */
const api = (baseURL: string, from: string) => apiRequest.newContext({ baseURL, extraHTTPHeaders: { 'X-Forwarded-For': from } });

/** Uses up from's 120 public API requests in this minute. */
async function useUpRequests(baseURL: string, from: string) {
	const a = await api(baseURL, from);
	for (let i = 0; i < 120; i++) {
		const r = await a.get('/api/v1/catalogue');
		if (r.status() === 429) break;
		expect(r.status()).toBe(200);
	}
	expect((await a.get('/api/v1/catalogue')).status()).toBe(429);
	await a.dispose();
}

/** A listener's socket from Node, as from (none: 127.0.0.1), open. */
function socket(baseURL: string, from?: string): Promise<WebSocket> {
	const url = baseURL.replace(/^http/, 'ws') + '/api/v1/live';
	// Node's WebSocket (undici) takes headers.
	const ws = new WebSocket(url, (from ? { headers: { 'X-Forwarded-For': from } } : undefined) as unknown as string[]);
	return new Promise((resolve, reject) => {
		ws.onopen = () => resolve(ws);
		ws.onerror = () => reject(new Error(`a socket as ${from ?? '127.0.0.1'} was refused`));
	});
}

const dock = (page: Page) => page.getByRole('region', { name: 'Now playing' });

/** Opens the Listen page with {1001} and presses Start listening. */
async function startListening(page: Page) {
	await page.addInitScript(() => localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] })));
	await page.goto('/');
	await expect(page.getByText('Talkgroups on (1): priority, remove and share')).toBeVisible();
	await page.getByRole('button', { name: 'Start listening' }).click();
}

/**
 * The page keeps room at its foot for a refused socket's notice (#147):
 * scrolled to the end, the page's last content, the Listen page's "Add
 * all" among it, is clear of the notice above the dock, so nothing stays
 * under it.
 */
async function endClearOfNotice(page: Page, width: number) {
	// The room follows the notice's height a frame after it's drawn.
	await expect
		.poll(
			() =>
				page.evaluate(() => {
					window.scrollTo(0, document.documentElement.scrollHeight);
					const top = document.querySelector('.notice-place')!.getBoundingClientRect().top;
					return [...document.querySelectorAll('main *:not(.notice-room)')]
						.filter((e) => {
							const r = e.getBoundingClientRect();
							return r.height > 0 && r.bottom > top;
						})
						.map((e) => `${e.tagName.toLowerCase()}.${e.className}: ${(e.textContent ?? '').trim().slice(0, 40)}`);
				}),
			{ message: `${width} px: the page's end is under the notice`, timeout: 5_000 }
		)
		.toEqual([]);
	await page.evaluate(() => window.scrollTo(0, 0));
}

async function axe(page: Page, width: number) {
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
}

/** The pull request's screenshots (RA_SHOTS names a folder), the whole page in one screen. */
async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS || test.info().project.name !== 'chromium') return;
	const size = page.viewportSize()!;
	await page.mouse.move(0, 0);
	await page.evaluate(() => window.scrollTo(0, 0));
	const height = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: size.width, height: Math.max(height, size.height) });
	await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}.png` });
	await page.setViewportSize(size);
}

const tooManyRequests = /^Too many requests from here: at most 120 a minute\. Try again in (\d+ seconds?|1 minute)\.$/;

/** The Recent calls page refused over the request limit: the talkgroups' load names the limit. */
async function recentRefused(browser: Browser, baseURL: string, look = 'harbour', theme = 'light', width = 1280) {
	const from = client();
	await useUpRequests(baseURL, from);
	const context = await contextAs(browser, baseURL, from, look, theme);
	const page = await context.newPage();
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/recent');
	await expect(page.getByRole('alert').filter({ hasText: 'Too many requests' }).first()).toHaveText(tooManyRequests);
	return { page, context };
}

test('A20 @limits: a 121st request in a minute shows its limit on the Recent calls page, naming when to try again', async ({ browser, baseURL }) => {
	const { page, context } = await recentRefused(browser, baseURL!);
	await expect(page.getByText("Couldn't load the talkgroups.")).toHaveCount(0);
	// Another client is unaffected.
	const other = await contextAs(browser, baseURL!, client());
	const fine = await other.newPage();
	await fine.goto('/recent');
	await expect(fine.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
	await expect(fine.getByText(/Too many requests/)).toHaveCount(0);
	await Promise.all([context.close(), other.close()]);
});

/** The basket page with up to 10 of 1001's calls in it, as from, and its zip's URL, with 2 zips of it being sent to from. */
async function basketWithTwoZips(browser: Browser, baseURL: string, look = 'harbour', theme = 'light', width = 1280) {
	const from = client();
	const a = await api(baseURL, from);
	const got = await a.get('/api/v1/recent-calls?talkgroups=1001&limit=20');
	expect(got.status(), await got.text()).toBe(200);
	const calls: { id: string; recording: unknown }[] = (await got.json()).calls;
	const ids = calls.filter((c) => c.recording).slice(0, 10).map((c) => c.id);
	expect(ids.length).toBeGreaterThanOrEqual(3);
	const context = await contextAs(browser, baseURL, from, look, theme);
	await context.addInitScript(
		(v) => localStorage.setItem('radioarchive.basket', v),
		JSON.stringify({ v: 1, calls: ids.map((id, i) => ({ id, added: 1_764_000_000_000 + i })), conversations: [] })
	);
	const page = await context.newPage();
	await page.setViewportSize({ width, height: 900 });
	await page.goto('/basket');
	const link = page.locator('main').getByRole('link', { name: /^Download the basket/ });
	await expect(link).toBeVisible();
	const href = (await link.getAttribute('href'))!;
	// Two zips of it, sent at 1 KB/s, so they're still being sent.
	const held = [a.get(href, { timeout: 120_000 }).catch(() => undefined), a.get(href, { timeout: 120_000 }).catch(() => undefined)];
	// Both start before anything else from that client is asked: a check
	// sent first would take one's place.
	await new Promise((r) => setTimeout(r, 1000));
	await expect.poll(async () => (await a.get(href, { headers: { Range: 'bytes=0-0' } })).status(), { timeout: 10_000 }).toBe(429);
	return { page, context, link, done: async () => (await a.dispose(), await Promise.all(held)) };
}

test('A20 @limits: a 3rd zip at once shows "Too many downloads at once…" by the button, and starts no download', async ({ browser, baseURL }) => {
	const { page, context, link, done } = await basketWithTwoZips(browser, baseURL!);
	let downloads = 0;
	page.on('download', () => downloads++);
	await link.click();
	await expect(page.getByRole('alert').filter({ hasText: 'Too many downloads' })).toHaveText(
		/^Too many downloads at once: at most 2 from here\. Try again in \d+ (seconds?|minutes?)\.$/
	);
	expect(downloads).toBe(0);
	await done();
	await context.close();
});

test('A20 @limits: a 5th live connection from one place shows its limit, and gets in once one closes', async ({ browser, baseURL }) => {
	test.setTimeout(120_000);
	// Everything here is 127.0.0.1: the page's socket and its check alike.
	const four = await Promise.all([0, 1, 2, 3].map(() => socket(baseURL!)));
	const context = await contextAs(browser, baseURL!, undefined);
	try {
		const page = await context.newPage();
		await startListening(page);
		await expect(page.getByText('Too many live connections from here: at most 4 at once. Trying again…')).toBeVisible({ timeout: 15_000 });
		for (const width of [1280, 375]) {
			await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
			await endClearOfNotice(page, width);
		}
		for (const ws of four) expect(ws.readyState).toBe(WebSocket.OPEN);
		// One closes: within the 30 s retry, the page is in.
		four[0].close();
		await expect(dock(page).getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 45_000 });
		await expect(page.getByText(/Too many live connections/)).toHaveCount(0);
	} finally {
		for (const ws of four) ws.close();
		await context.close();
	}
});

test('A20 @limits: with 50 sockets open a new listener sees "The radio is full right now. Trying again…", none of the 50 drops, and it gets in once one closes; axe and the screenshots', async ({ browser, baseURL }) => {
	test.setTimeout(240_000);
	// 50 from 13 clients.
	const from = Array.from({ length: 13 }, client);
	const fifty = await Promise.all(Array.from({ length: 50 }, (_, i) => socket(baseURL!, from[Math.floor(i / 4)])));
	try {
		const context = await contextAs(browser, baseURL!, client());
		const page = await context.newPage();
		await startListening(page);
		const full = page.getByText('The radio is full right now. Trying again…');
		await expect(full).toBeVisible({ timeout: 15_000 });

		// Axe and the screenshots, in both looks and both themes, while it's full.
		for (const look of looks)
			for (const theme of themes)
				for (const width of widths) {
					const c = await contextAs(browser, baseURL!, client(), look, theme);
					const p = await c.newPage();
					await p.setViewportSize({ width, height: 900 });
					await startListening(p);
					await expect(p.getByText('The radio is full right now. Trying again…')).toBeVisible({ timeout: 15_000 });
					await axe(p, width);
					await endClearOfNotice(p, width);
					await shot(p, `radio-full-${look}-${theme}-${width}`);
					await c.close();
				}

		for (const ws of fifty) expect(ws.readyState).toBe(WebSocket.OPEN);
		// One closes: within the 30 s retry, the waiting listener is in.
		fifty[0].close();
		await expect(dock(page).getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 45_000 });
		await expect(full).toHaveCount(0);
		for (const ws of fifty.slice(1)) expect(ws.readyState).toBe(WebSocket.OPEN);
		await context.close();
	} finally {
		for (const ws of fifty) ws.close();
	}
});

test('A20 @limits: axe and the screenshots of a search and a download refused, in both looks and both themes at 375 px and 1,280 px', async ({ browser, baseURL }) => {
	test.setTimeout(300_000);
	for (const look of looks)
		for (const theme of themes)
			for (const width of widths) {
				const r = await recentRefused(browser, baseURL!, look, theme, width);
				await axe(r.page, width);
				await shot(r.page, `search-refused-${look}-${theme}-${width}`);
				await r.context.close();

				const z = await basketWithTwoZips(browser, baseURL!, look, theme, width);
				await z.link.click();
				await expect(z.page.getByRole('alert').filter({ hasText: 'Too many downloads' })).toBeVisible();
				await axe(z.page, width);
				await shot(z.page, `download-refused-${look}-${theme}-${width}`);
				await z.done();
				await z.context.close();
			}
});
