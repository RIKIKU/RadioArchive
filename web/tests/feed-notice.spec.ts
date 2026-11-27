/// <reference types="node" />
// The recorder notice can be put away (SPEC.md R15, A27; ADR 0050): by its
// close button or Escape, or by itself after 10 s on screen, held while
// it's hovered or focused. Put away, it stays away for the browser session,
// on every page and through a reload or a new socket, until the server
// says it's off; the next outage shows it again.
//
// The live feed here is the test's own (page.routeWebSocket), so it says
// what the server would, in the server's order (ADR 0007, 0010): the
// welcome, the notice if it's on, then "listening" once the set comes.
// No recorder has to go deaf, so these aren't @live.
import { test, expect, type Page, type WebSocketRoute } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const NOTICE = "Some talkgroups aren't being received right now.";
const KEY = 'radioarchive.recorder-notice-dismissed';
const notice = (page: Page) => page.locator('.notice-place').getByText(NOTICE);
const close = (page: Page) => page.getByRole('button', { name: `Dismiss: ${NOTICE}` });
const kept = (page: Page) => page.evaluate((k) => sessionStorage.getItem(k), KEY);

/** The page's live feed, as the test says it. */
class Feed {
	recorders = false;
	sockets: WebSocketRoute[] = [];
	/** How many sets the feed has taken, on every socket. */
	listens = 0;

	async route(page: Page) {
		await page.routeWebSocket(/\/api\/v1\/live$/, (ws) => {
			this.sockets.push(ws);
			ws.send(JSON.stringify({ type: 'welcome', protocol: 1, last_event_id: 0 }));
			if (this.recorders) ws.send(this.#msg(true));
			ws.onMessage((m) => {
				if (JSON.parse(String(m)).type !== 'set') return;
				ws.send(JSON.stringify({ type: 'listening' }));
				this.listens++;
			});
		});
	}

	/** The recorder notice goes on or off, sent to the open socket. */
	set(on: boolean) {
		this.recorders = on;
		this.sockets.at(-1)?.send(this.#msg(on));
	}

	/** The socket drops; the page connects again by itself and is listening. */
	async drop() {
		const [sockets, listens] = [this.sockets.length, this.listens];
		await this.sockets.at(-1)?.close();
		await expect.poll(() => this.sockets.length, { timeout: 20_000 }).toBeGreaterThan(sockets);
		await expect.poll(() => this.listens, { timeout: 20_000 }).toBeGreaterThan(listens);
		// The page has read what came.
		await new Promise((r) => setTimeout(r, 300));
	}

	#msg(active: boolean) {
		return JSON.stringify({ type: 'notice', notice: 'recorders', active });
	}
}

async function start(page: Page, feed: Feed, path = '/') {
	await page.addInitScript(() => localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] })));
	await feed.route(page);
	await page.goto(path);
	await listen(page);
}

async function listen(page: Page) {
	await page.getByRole('button', { name: 'Start listening' }).click();
	await expect(page.getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });
}

test('R15: the close button puts the recorder notice away, and the focus goes on to the content', async ({ page }) => {
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	// Announced once as it comes: the notice place is a polite status region.
	await expect(page.locator('.notice-place')).toHaveAttribute('role', 'status');
	await close(page).click();
	await expect(notice(page)).toHaveCount(0);
	expect(await kept(page)).toBe('1');
	expect(await page.evaluate(() => document.activeElement?.id)).toBe('main');
});

test('R15: put away, it stays away on other pages, through a new socket and through a reload', async ({ page }) => {
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await close(page).click();
	// Another page: the player and its feed go on.
	await page.locator('nav.side').getByRole('link', { name: 'Recent calls' }).click();
	await expect(page).toHaveURL(/\/recent$/);
	await expect(notice(page)).toHaveCount(0);
	// A new socket starts with every notice off, then the server says it's on again: the same outage.
	await feed.drop();
	await expect(page.getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });
	await expect(notice(page)).toHaveCount(0);
	// A reload: the browser session keeps it.
	await page.reload();
	await listen(page);
	await page.waitForTimeout(500);
	await expect(notice(page)).toHaveCount(0);
	expect(await kept(page)).toBe('1');
});

test('R15: once the server says the notice is off, the next outage shows it again', async ({ page }) => {
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await close(page).click();
	feed.set(false);
	await expect.poll(() => kept(page)).toBeNull();
	feed.set(true);
	await expect(notice(page)).toBeVisible();
});

test("R15: an outage that ended while the socket was down counts as over: a socket listening without the notice", async ({ page }) => {
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await close(page).click();
	// It ends while the page is away: the new socket's welcome and listening come without it.
	feed.recorders = false;
	await feed.drop();
	await expect.poll(() => kept(page)).toBeNull();
	feed.set(true);
	await expect(notice(page)).toBeVisible();
});

test('R15: the close button is reached with Tab, right after Skip to content, and Enter or Escape puts the notice away', async ({ page }) => {
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	// The page's first stop is Skip to content; the next is the close button.
	await page.getByRole('link', { name: 'Skip to content' }).focus();
	await page.keyboard.press('Tab');
	await expect(close(page)).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(notice(page)).toHaveCount(0);
	expect(await page.evaluate(() => document.activeElement?.id)).toBe('main');
	// The next Tab goes on into the content, not back to the top.
	await page.keyboard.press('Tab');
	expect(await page.evaluate(() => !!document.activeElement?.closest('main'))).toBe(true);

	// A new outage; Escape with the focus in it puts it away too.
	feed.set(false);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	await close(page).focus();
	await page.keyboard.press('Escape');
	await expect(notice(page)).toHaveCount(0);
	expect(await kept(page)).toBe('1');
});

test('R15: it goes by itself after 10 s, which counts as putting it away', async ({ page }) => {
	await page.clock.install();
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	await page.clock.runFor(9_000);
	await expect(notice(page)).toBeVisible();
	await page.clock.runFor(1_500);
	await expect(notice(page)).toHaveCount(0);
	expect(await kept(page)).toBe('1');
	// The same outage on a new socket: still away.
	await feed.drop();
	await expect(page.getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });
	await expect(notice(page)).toHaveCount(0);
});

test('R15: hovering or focusing the notice holds the 10 s, and leaving starts it again', async ({ page }) => {
	// The player's own timers run every 20 ms of the clock, so a minute of
	// it takes WebKit a while.
	test.setTimeout(90_000);
	await page.clock.install();
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	await page.clock.runFor(8_000);
	// Hovered: it waits as long as the pointer is on it.
	await notice(page).hover();
	await page.clock.runFor(15_000);
	await expect(notice(page)).toBeVisible();
	// Left: a whole 10 s again, more than the 2 s it had left.
	await page.mouse.move(1, 1);
	await page.clock.runFor(7_000);
	await expect(notice(page)).toBeVisible();
	// Focused: the same.
	await close(page).focus();
	await page.clock.runFor(15_000);
	await expect(notice(page)).toBeVisible();
	await page.locator('h1').first().click();
	await page.clock.runFor(7_000);
	await expect(notice(page)).toBeVisible();
	await page.clock.runFor(4_000);
	await expect(notice(page)).toHaveCount(0);
});

test('axe: the recorder notice in both looks and both themes at 375 px and 1,280 px: above the dock, inside the page, and the screenshots', async ({ page }, info) => {
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await page.clock.install();
	const feed = new Feed();
	await start(page, feed);
	feed.set(true);
	await expect(notice(page)).toBeVisible();
	// Held, so it stays while it's checked.
	await notice(page).hover();
	for (const width of [1280, 375]) {
		await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
		await notice(page).hover();
		for (const look of ['harbour', 'console'])
			for (const theme of ['light', 'dark']) {
				await page.evaluate(
					([l, t]) => {
						const d = document.documentElement.dataset;
						if (l === 'console') d.look = l;
						else delete d.look;
						d.theme = t;
					},
					[look, theme]
				);
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']).analyze();
				expect(results.violations, `${look} ${theme}, ${width} px`).toEqual([]);
				// Above the dock and the bottom bar, inside the screen, no sideways scroll.
				const box = (await page.locator('.notice-place').boundingBox())!;
				const dock = (await page.getByRole('region', { name: 'Now playing' }).boundingBox())!;
				expect(box.y + box.height, `${width} px: above the dock`).toBeLessThanOrEqual(dock.y);
				expect(box.x).toBeGreaterThanOrEqual(0);
				expect(box.x + box.width).toBeLessThanOrEqual(width);
				expect(await page.evaluate(() => document.documentElement.scrollWidth), `${width} px scrolls sideways`).toBeLessThanOrEqual(width);
				// The close button is a whole target.
				const b = (await close(page).boundingBox())!;
				expect(b.height).toBeGreaterThanOrEqual(36);
				expect(b.width).toBeGreaterThanOrEqual(36);
				if (shots && look === 'harbour') await page.screenshot({ path: `${shots}/notice-${theme}-${width === 375 ? 'phone' : 'desktop'}.png` });
			}
		// The page keeps room for it at its foot: scrolled to the end, the
		// page's last content is clear of it.
		const clear = await page.evaluate(() => {
			window.scrollTo(0, document.documentElement.scrollHeight);
			const content = [...document.querySelectorAll('main *:not(.notice-room)')].map((e) => e.getBoundingClientRect()).filter((r) => r.height > 0);
			return Math.max(...content.map((r) => r.bottom)) <= document.querySelector('.notice-place')!.getBoundingClientRect().top;
		});
		expect(clear, `${width} px: the page's end clear of the notice`).toBe(true);
		await page.evaluate(() => window.scrollTo(0, 0));
		await notice(page).hover();
	}
});
