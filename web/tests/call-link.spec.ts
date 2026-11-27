/// <reference types="node" />
// Call links (issue #80; SPEC.md R24, R33, R45, R46, R61, R62, R74; A10,
// A16; ADR 0017), against the binary with the fixture's made-up archive
// (testing/webfixture/calls.go): the call card for an ended call (A17's,
// on 1001, unit 2099) and for one in progress (on 1002, unit 3002), the
// one card for every unavailable call, the link's preview, Share from a
// row, and axe. The API runs in TZ=Australia/Melbourne; the browsers in
// UTC. A16 itself, which follows a call through its life, and Share from
// now playing, are call-link-live.spec.ts's (@live).
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const ZONE = 'Australia/Melbourne';
const NEVER = '6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f';

const card = (page: Page) => page.locator('.card');
const below = (page: Page, name: string) => page.getByRole('list', { name: `Calls on ${name}` }).getByRole('listitem');

/** The one call a unit was heard on, as the API gives it. */
async function callOf(page: Page, unit: number) {
	return page.evaluate(async (u) => (await (await fetch(`/api/v1/recent-calls?unit=${u}`)).json()).calls[0], unit);
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

/** Nothing on the page names hidden 1003 or ignored 1004. */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004']) expect(text, s).not.toContain(s);
}

test("an ended call's card: its talkgroup, group, start in TZ, duration, units, Play, Download, Share; live listening doesn't start", async ({ page }) => {
	await page.goto('/');
	const c = await callOf(page, 2099);
	await page.goto(`/calls/${c.id}`);
	await expect(page.getByRole('heading', { level: 1, name: 'Dispatch North' })).toBeVisible();
	await expect(card(page)).toHaveAttribute('data-state', 'recorded');
	await expect(card(page).locator('.where')).toHaveText('North');
	const full = new Intl.DateTimeFormat('en-AU', { timeZone: ZONE, weekday: 'long', day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(c.started_at));
	await expect(card(page).locator('time')).toHaveText(full);
	await expect(card(page)).toContainText('Melbourne time');
	await expect(card(page)).toContainText('0:03');
	await expect(card(page).locator('.units')).toHaveText('2099');
	await expect(card(page).getByRole('button', { name: /^Play: Dispatch North/ })).toBeVisible();
	await expect(card(page).getByRole('link', { name: /^Download: Dispatch North/ })).toHaveAttribute('href', `/api/v1/calls/${c.id}/download`);
	await expect(card(page).getByRole('button', { name: /^Share: Dispatch North/ })).toBeVisible();
	await expect(page).toHaveTitle('Dispatch North · RadioArchive');
	// Live listening doesn't start; Start listening stays in the dock.
	await expect(page.getByRole('region', { name: 'Player' }).getByRole('button', { name: 'Start listening' })).toBeVisible();
	// Below: its talkgroup's calls from it on, oldest first, starting with it.
	const rows = below(page, 'Dispatch North');
	await expect(rows.first()).toHaveAttribute('data-call-id', c.id);
	await expect(rows.first()).toContainText('This call');
	expect(await rows.count()).toBeGreaterThan(1);
	for (const t of await rows.locator('.tg').allInnerTexts()) expect(t).toBe('Dispatch North');
	const times = await rows.locator('time').evaluateAll((els) => els.map((e) => e.getAttribute('datetime')!));
	expect(times).toEqual([...times].sort());
	await expect(page.getByRole('link', { name: 'All recent calls on Dispatch North' })).toHaveAttribute('href', '/recent?talkgroups=1001');
	await namesNothingHidden(page);
});

test('a call in progress: the card says so and offers live listening, with no Play or Download', async ({ page }) => {
	await page.goto('/');
	const c = await callOf(page, 3002);
	expect(c.in_progress).toBe(true);
	await page.goto(`/calls/${c.id}`);
	await expect(page.getByRole('heading', { level: 1, name: 'Fireground 41' })).toBeVisible();
	await expect(card(page)).toHaveAttribute('data-state', 'in-progress');
	await expect(card(page).locator('.progress')).toHaveText('In progress');
	await expect(card(page).getByRole('button', { name: 'Listen live' })).toBeVisible();
	await expect(card(page).getByRole('button', { name: /^Play/ })).toHaveCount(0);
	await expect(card(page).getByRole('link', { name: /^Download/ })).toHaveCount(0);
	await expect(card(page)).toContainText('Once the call ends, this link plays its recording.');
	await expect(page.getByRole('region', { name: 'Player' }).getByRole('button', { name: 'Start listening' })).toBeVisible();
});

test("an unavailable call: one card naming nothing, the app's own page with one status, whatever the reason", async ({ page, request }) => {
	const shell = await (await request.get('/')).text();
	const answers = [];
	for (const id of [NEVER, '1003', 'not-a-call', NEVER.toUpperCase().replace('F', 'E')]) {
		const r = await request.get(`/calls/${id}`);
		answers.push({ status: r.status(), body: await r.text(), type: r.headers()['content-type'], robots: r.headers()['x-robots-tag'] });
	}
	for (const a of answers) {
		expect(a).toEqual(answers[0]);
		expect(a.status).toBe(404);
		expect(a.body).toBe(shell);
		expect(a.robots).toBe('noindex');
	}
	let markup = '';
	for (const id of [NEVER, '1003', 'not-a-call']) {
		await page.goto(`/calls/${id}`);
		await expect(page.getByRole('heading', { level: 1, name: "This call isn't available" })).toBeVisible();
		await namesNothingHidden(page);
		const m = await page.locator('main').innerHTML();
		if (markup) expect(m).toBe(markup);
		markup = m;
	}
});

test("the link's preview names the talkgroup and the time, and holds no audio", async ({ page, request }) => {
	await page.goto('/');
	const c = await callOf(page, 2099);
	const r = await request.get(`/calls/${c.id}`);
	expect(r.status()).toBe(200);
	expect(r.headers()['x-robots-tag']).toBe('noindex');
	const html = await r.text();
	expect(html).toContain('<meta property="og:title" content="Dispatch North" />');
	const p = Object.fromEntries(
		new Intl.DateTimeFormat('en-US', { timeZone: ZONE, weekday: 'short', day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
			.formatToParts(new Date(c.started_at))
			.map((x) => [x.type, x.value])
	);
	const desc = html.match(/<meta property="og:description" content="([^"]*)" \/>/)?.[1] ?? '';
	expect(desc).toMatch(new RegExp(`^${p.weekday} ${p.day} ${p.month} ${p.year}, ${p.hour}:${p.minute}:${p.second} AE[DS]T$`));
	for (const s of ['/api/', 'recording', '.opus', 'og:audio', 'og:video', c.id]) expect(html, s).not.toContain(s);
});

test('Share on a recent-calls row copies the call\'s link, which opens its card', async ({ page }) => {
	await catchCopies(page);
	await page.goto('/recent?unit=2099');
	const row = page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem').first();
	const id = await row.getAttribute('data-call-id');
	await row.getByRole('button', { name: /^Share: Dispatch North/ }).click();
	await expect(row.getByText('Link copied')).toBeVisible();
	const origin = new URL(page.url()).origin;
	expect(await copied(page)).toEqual([`${origin}/calls/${id}`]);
	await page.goto((await copied(page))[0]);
	await expect(card(page)).toHaveAttribute('data-call-id', id!);
	await expect(page.getByRole('heading', { level: 1, name: 'Dispatch North' })).toBeVisible();
});

test("Share where the browser won't copy shows the link to copy by hand", async ({ page }) => {
	await page.addInitScript(() => {
		Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => Promise.reject(new Error('no')) } });
	});
	await page.goto('/recent?unit=2099');
	const row = page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem').first();
	const id = await row.getAttribute('data-call-id');
	await row.getByRole('button', { name: /^Share/ }).click();
	const field = row.getByRole('textbox', { name: 'Copy this link' });
	await expect(field).toBeFocused();
	await expect(field).toHaveValue(`${new URL(page.url()).origin}/calls/${id}`);
});

test('axe: an ended, an in-progress and an unavailable call, in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await page.goto('/');
	const ended = await callOf(page, 2099);
	const live = await callOf(page, 3002);
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
						await page.screenshot({ path: `${shots}/call-${state}-${look}-${theme}-${width}.png` });
					}
				}
		}
	};
	await page.goto(`/calls/${ended.id}`);
	await check('ended', () => expect(below(page, 'Dispatch North').first()).toBeVisible());
	await page.goto(`/calls/${live.id}`);
	await check('in-progress', () => expect(below(page, 'Fireground 41').first()).toBeVisible());
	await page.goto(`/calls/${NEVER}`);
	await check('unavailable', () => expect(page.getByRole('heading', { name: "This call isn't available" })).toBeVisible());
});
