/// <reference types="node" />
// Sharing a conversation (issue #82; SPEC.md P4, R33, R66, R74; ADR 0020),
// against the binary with the fixture's made-up archive
// (testing/webfixture/calls.go): Share on a basket's conversation copies a
// link that opens its name, note and calls as cards in order; the name and
// note are text, never markup; every link that isn't available is one
// page; a link at the URL budget opens; and axe. Hidden calls and Play all
// are conversation-live.spec.ts's (@live).
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { conversationPath, MAX_LINK } from '../src/lib/share/conversation.ts';

const KEY = 'radioarchive.basket';
const NEVER = '6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f';
const NAME = 'Structure fire';
const NOTE = 'Pumper 1 first on scene.\nSecond alarm at 14:10.';

const cards = (page: Page) => page.locator('.cards > li > article');

/** Three of the fixture's calls on 1001, oldest first. */
async function threeCalls(page: Page): Promise<{ id: string; started_at: string }[]> {
	await page.goto('/');
	const got = await page.evaluate(async () => (await (await fetch('/api/v1/recent-calls?talkgroups=1001&limit=3')).json()).calls);
	return got.reverse();
}

/** Copying records the text instead, in window.copied. */
async function catchCopies(page: Page) {
	await page.addInitScript(() => {
		const w = window as unknown as { copied: string[] };
		w.copied = [];
		Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => void w.copied.push(t) } });
	});
}
const copied = (page: Page) => page.evaluate(() => (window as unknown as { copied: string[] }).copied);

/** A basket holding one conversation of ids, in this browser, before the page loads. */
async function withConversation(page: Page, ids: string[], name = NAME, note = NOTE) {
	const kept = {
		v: 1,
		calls: ids.map((id, i) => ({ id, added: 1_764_000_000_000 + i })),
		conversations: [{ id: 'c1', name, note, calls: ids.map((id, i) => ({ id, added: 1_764_000_000_100 + i })) }]
	};
	await page.addInitScript(([k, v]) => localStorage.setItem(k, v), [KEY, JSON.stringify(kept)] as const);
}

/** The link's markup that may differ: none. The page's main, as rendered. */
const markup = (page: Page) => page.locator('main').innerHTML();

test('Share on a conversation copies a link that opens its name, note and three cards in order', async ({ page }) => {
	await catchCopies(page);
	const three = await threeCalls(page);
	const ids = three.map((c) => c.id);
	await withConversation(page, ids);
	await page.goto('/basket');
	const conv = page.getByRole('article', { name: NAME });
	await expect(conv.locator('[data-state="recorded"]')).toHaveCount(3);
	await conv.getByRole('button', { name: `Share: ${NAME}` }).click();
	await expect(conv.getByText('Link copied')).toBeVisible();
	const [link] = await copied(page);
	const origin = new URL(page.url()).origin;
	expect(link).toBe(origin + conversationPath({ name: NAME, note: NOTE, calls: ids }));
	// What the server sees of the link: /conversation, the app's own page.
	expect(new URL(link).pathname).toBe('/conversation');

	await page.goto(link);
	await expect(page.getByRole('heading', { level: 1, name: NAME })).toBeVisible();
	await expect(page.locator('.head .note')).toHaveText(NOTE);
	await expect(page).toHaveTitle(`${NAME} · RadioArchive`);
	await expect(cards(page)).toHaveCount(3);
	expect(await cards(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-call-id')))).toEqual(ids);
	for (const c of await cards(page).all()) await expect(c.locator('.tg')).toHaveText('Dispatch North');
	await expect(page.getByRole('button', { name: 'Play all' })).toBeVisible();
	await expect(cards(page).first().getByRole('link', { name: /^Open the call/ })).toHaveAttribute('href', `/calls/${ids[0]}`);
	// Live listening doesn't start.
	await expect(page.getByRole('region', { name: 'Now playing' }).getByRole('button', { name: 'Start listening' })).toBeVisible();
});

test("a name and note holding markup show as the text typed; nothing in them runs or renders", async ({ page }) => {
	const [c] = await threeCalls(page);
	const name = '<img src=x onerror="window.pwned=1"> Fire</h1><h1>Two';
	const note = '<b>bold</b> <a href="https://example.invalid/">a link</a>\n<script>window.pwned=2</script>';
	await page.goto(conversationPath({ name, note, calls: [c.id] }));
	await expect(page.locator('h1')).toHaveText(name);
	await expect(page.locator('.head .note')).toHaveText(note);
	await expect(page).toHaveTitle(`${name} · RadioArchive`);
	await expect(page.locator('main img, main b, main script, main .head a')).toHaveCount(0);
	expect(await page.evaluate(() => (window as unknown as { pwned?: number }).pwned)).toBeUndefined();
});

test("a link that isn't available: one page, naming nothing, whatever the reason, and the server never sees the list", async ({ page, request }) => {
	// The server's answer for /conversation is the app's own page, the same
	// for every link: the list is in the fragment, never sent.
	const shell = await (await request.get('/')).text();
	const r = await request.get('/conversation');
	expect(r.status()).toBe(200);
	expect(await r.text()).toBe(shell);
	let first = '';
	for (const link of [
		conversationPath({ name: NAME, note: NOTE, calls: [NEVER] }),
		conversationPath({ name: 'Another name', note: '', calls: [NEVER, '00000000-0000-4000-8000-000000000001'] }),
		'/conversation',
		'/conversation#',
		'/conversation#not-a-conversation'
	]) {
		await page.goto('/');
		await page.goto(link);
		await expect(page.getByRole('heading', { level: 1, name: "This conversation isn't available" })).toBeVisible();
		await expect(page).toHaveTitle("Conversation isn't available · RadioArchive");
		const text = await page.locator('body').innerText();
		for (const s of [NAME, 'Another name', 'Pumper']) expect(text, s).not.toContain(s);
		const m = await markup(page);
		if (first) expect(m, link).toBe(first);
		first = m;
	}
});

test('a conversation at the URL budget opens, with the calls still available', async ({ page }) => {
	const three = await threeCalls(page);
	const ids = three.map((c) => c.id);
	// Never-stored calls fill the link up to the budget, the real ones among them.
	const fill = (n: number) => Array.from({ length: n }, (_, i) => `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`);
	let n = 1;
	const path = (k: number) => conversationPath({ name: NAME, note: '', calls: [ids[0], ...fill(k), ids[1], ids[2]] });
	while (path(n + 1).length <= MAX_LINK) n++;
	expect(path(n).length).toBeGreaterThan(MAX_LINK - 30);
	await page.goto(path(n));
	await expect(page.getByRole('heading', { level: 1, name: NAME })).toBeVisible();
	expect(await cards(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-call-id')))).toEqual(ids);
	await expect(page.locator('.head .meta')).toContainText('3 calls');
});

test('axe: a shared conversation and an unavailable one, in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await catchCopies(page);
	const three = await threeCalls(page);
	const ids = three.map((c) => c.id);
	const check = async (state: string, ready: () => Promise<void>) => {
		for (const width of [1280, 375]) {
			await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
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
					await ready();
					const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
					expect(results.violations, `${state}, ${look} ${theme}, ${width} px`).toEqual([]);
					expect(await page.evaluate(() => document.documentElement.scrollWidth), `${state} at ${width} px scrolls sideways`).toBeLessThanOrEqual(width);
					if (shots) {
						await page.mouse.move(0, 0);
						await page.screenshot({ path: `${shots}/conversation-${state}-${look}-${theme}-${width}.png`, fullPage: state !== 'basket' });
					}
				}
		}
	};
	// Sharing it from the basket, "Link copied".
	await withConversation(page, ids);
	await page.goto('/basket');
	const conv = page.getByRole('article', { name: NAME });
	await expect(conv.locator('[data-state="recorded"]')).toHaveCount(3);
	await conv.getByRole('button', { name: `Share: ${NAME}` }).click();
	await check('basket', async () => {
		await conv.getByRole('button', { name: `Share: ${NAME}` }).click();
		await expect(conv.getByText('Link copied')).toBeVisible();
		await conv.scrollIntoViewIfNeeded();
	});
	await page.goto(conversationPath({ name: NAME, note: NOTE, calls: ids }));
	await check('shared', () => expect(cards(page)).toHaveCount(3));
	await page.goto(conversationPath({ name: NAME, note: NOTE, calls: [NEVER] }));
	await check('unavailable', () => expect(page.getByRole('heading', { name: "This conversation isn't available" })).toBeVisible());
});
