/// <reference types="node" />
// The prompts that ask which listening set to keep (SPEC.md R41; ADR 0008,
// ADR 0009), with sets as big as a real deployment's: an account holding
// 1,200 talkgroups against a browser holding 200 others, and a share link
// naming 1,200. The question and its buttons are at the top, in view
// without scrolling on a phone and a desktop; the sets are counts, with
// what differs folded and only a few names of it. The catalogue and the
// account are served in place of the API's, so no sign-in is needed: the
// account's real round trip is web/signin-tests/a33.spec.ts's.
import { test, expect, type Locator, type Page, type Route } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const SAVED = 'radioarchive.listening-set';
const LINKED = 'radioarchive.account';
const ID = 'made-up-account-7';

type Entry = { id: number; priority?: boolean };

const talkgroup = (id: number) => ({ id, label: `TG${id}`, name: `Channel ${id}`, tag: 'dispatch', groups: [`Agency ${Math.ceil(id / 100)}`], led_colour: null, alert: null, delay_minutes: 0, fireground_channel: null });

// 1,300 public talkgroups, 1 to 1,300.
const catalogue = {
	groups: Array.from({ length: 13 }, (_, i) => `Agency ${i + 1}`),
	tags: ['dispatch'],
	talkgroups: Array.from({ length: 1300 }, (_, i) => talkgroup(i + 1))
};

const range = (from: number, to: number, priority: (id: number) => boolean = () => false): Entry[] =>
	Array.from({ length: to - from + 1 }, (_, i) => from + i).map((id) => (priority(id) ? { id, priority: true } : { id }));

// The account: 1 to 1,200, every 120th marked priority (10). The browser:
// 1,101 to 1,300, 1,101 to 1,103 marked (3). 100 (1,101 to 1,200) in
// both, 4 of them marked on one side only: 1,101 to 1,103 in the browser,
// 1,200 in the account.
const accountSet = range(1, 1200, (id) => id % 120 === 0);
const browserSet = range(1101, 1300, (id) => id <= 1103);

const sizes = [
	{ width: 375, height: 667 },
	{ width: 1280, height: 800 }
] as const;

/** Serves the catalogue and an account holding `set`, and records what the page saves to it. */
async function open(page: Page, opts: { linked: boolean; set?: Entry[]; path?: string }) {
	const puts: Entry[][] = [];
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
	await page.route('**/api/v1/account', (r) =>
		r.fulfill({
			json: { signin: true, account: { id: ID, name: 'Made-up Listener' }, saves: true, profile: { v: 1, talkgroups: opts.set ?? accountSet, alerts: false } }
		})
	);
	await page.route('**/api/v1/account/profile', async (r: Route) => {
		if (r.request().method() === 'PUT') puts.push((r.request().postDataJSON() as { talkgroups: Entry[] }).talkgroups);
		await r.fulfill({ status: 204 });
	});
	// Once per tab, so a reload keeps what the page changed.
	await page.addInitScript(
		([k, s, l, id]) => {
			if (sessionStorage.getItem('seeded')) return;
			sessionStorage.setItem('seeded', '1');
			localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s }));
			if (l) localStorage.setItem(l as string, id as string);
		},
		[SAVED, browserSet, opts.linked ? LINKED : '', ID] as const
	);
	await page.goto(opts.path ?? '/');
	return puts;
}

const saved = (page: Page) => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? 'null')?.talkgroups as Entry[], SAVED);
const linked = (page: Page) => page.evaluate((k) => localStorage.getItem(k), LINKED);

/** A control is on screen with the page unscrolled, and nothing (the dock, the bottom bar) covers its middle. */
async function inViewUnscrolled(page: Page, control: Locator) {
	expect(await page.evaluate(() => window.scrollY)).toBe(0);
	const box = (await control.boundingBox())!;
	const size = page.viewportSize()!;
	expect(box.y + box.height, 'above the screen\'s foot').toBeLessThanOrEqual(size.height);
	const hit = await control.evaluate((el, [x, y]) => {
		const at = document.elementFromPoint(x, y);
		return !!at && (at === el || el.contains(at));
	}, [box.x + box.width / 2, box.y + box.height / 2]);
	expect(hit, 'not covered').toBe(true);
}

async function axe(page: Page, what: string) {
	for (const theme of ['light', 'dark']) {
		await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
		const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
		expect(results.violations, `${what}, ${theme}`).toEqual([]);
		expect(await page.evaluate(() => document.documentElement.scrollWidth), `${what}: no sideways scroll`).toBeLessThanOrEqual(page.viewportSize()!.width);
	}
}

/** A screenshot of the screen as it opens, when RA_SHOTS names a folder. */
async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS || test.info().project.name !== 'chromium') return;
	for (const theme of ['light', 'dark']) {
		await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
		await page.mouse.move(0, 0);
		await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}-${theme}-${page.viewportSize()!.width}.png` });
	}
}

for (const size of sizes)
	test(`R41: the account's prompt, 1,200 talkgroups against 200: counts, then the buttons in view, what differs folded, ${size.width}px`, async ({ page }) => {
		await page.setViewportSize(size);
		await open(page, { linked: false });
		const prompt = page.getByRole('region', { name: 'Use the listening set saved to your account?' });
		await expect(prompt).toContainText('Your account has 1,200 talkgroups (10 priority). This browser has 200 talkgroups (3 priority). Nothing is merged: choose one.');
		for (const name of ["Use my account's set", "Keep this browser's set"]) await inViewUnscrolled(page, prompt.getByRole('button', { name }));
		// No names until asked for.
		await expect(prompt.getByRole('listitem').first()).toBeHidden();
		await axe(page, `the account's prompt, ${size.width}px`);
		await shot(page, 'account-prompt');

		// What differs: counts, then a few names of each side, never the set.
		await prompt.getByText('Show the differences').click();
		await expect(prompt).toContainText('100 in both, 4 priority marks different.');
		const onlyAccount = prompt.getByRole('list', { name: 'Only in your account' });
		await expect(onlyAccount.getByRole('listitem')).toHaveCount(12);
		await expect(onlyAccount.getByRole('listitem').first()).toHaveText('Channel 1');
		await expect(prompt).toContainText('Only in your account (1,100)');
		await expect(prompt).toContainText('and 1,088 more');
		const onlyBrowser = prompt.getByRole('list', { name: 'Only in this browser' });
		await expect(onlyBrowser.getByRole('listitem')).toHaveCount(12);
		await expect(prompt).toContainText('Only in this browser (100)');
		await expect(prompt).toContainText('and 88 more');
		await expect(prompt.getByRole('listitem')).toHaveCount(24);
		await axe(page, `the account's prompt, opened, ${size.width}px`);
		await shot(page, 'account-prompt-opened');
	});

test("R41: Use my account's set: the browser takes the account's set, saves nothing, and isn't asked again", async ({ page }) => {
	const puts = await open(page, { linked: false });
	const prompt = page.getByRole('region', { name: 'Use the listening set saved to your account?' });
	await prompt.getByRole('button', { name: "Use my account's set" }).click();
	await expect(prompt).toHaveCount(0);
	expect(await saved(page)).toEqual(accountSet);
	expect(await linked(page)).toBe(ID);
	await page.reload();
	await expect(page.getByRole('heading', { name: 'Your listening set' })).toBeVisible();
	await page.waitForTimeout(500);
	await expect(prompt).toHaveCount(0);
	expect(puts).toEqual([]);
});

test("R41: Keep this browser's set: the account takes the browser's, and the browser keeps it", async ({ page }) => {
	const puts = await open(page, { linked: false });
	const prompt = page.getByRole('region', { name: 'Use the listening set saved to your account?' });
	await prompt.getByRole('button', { name: "Keep this browser's set" }).click();
	await expect(prompt).toHaveCount(0);
	await expect.poll(() => puts.length).toBe(1);
	expect(puts[0]).toEqual(browserSet);
	expect(await saved(page)).toEqual(browserSet);
	expect(await linked(page)).toBe(ID);
});

test('R41: a browser matched with the account before is never asked: a set changed on another device is simply its set now', async ({ page }) => {
	// Another device (the Android app, ADR 0052) kept its own set, so the
	// account's is now different from this browser's.
	const puts = await open(page, { linked: true });
	await expect.poll(() => saved(page)).toEqual(accountSet);
	await expect(page.getByRole('heading', { name: 'Talkgroups on (1,200)' })).toBeVisible();
	await expect(page.getByRole('region', { name: 'Use the listening set saved to your account?' })).toHaveCount(0);
	expect(puts).toEqual([]);
});

test('R41: a first sign-in where the account holds the browser\'s set already asks nothing', async ({ page }) => {
	await open(page, { linked: false, set: browserSet });
	await expect.poll(() => linked(page)).toBe(ID);
	await expect(page.getByRole('region', { name: 'Use the listening set saved to your account?' })).toHaveCount(0);
});

for (const size of sizes)
	test(`R41: a share link naming 1,200 talkgroups: counts, then the buttons in view, ${size.width}px`, async ({ page }) => {
		await page.setViewportSize(size);
		await page.route('**/api/v1/account', (r) => r.fulfill({ json: { signin: true, account: null, saves: false, profile: null } }));
		await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
		await page.addInitScript(([k, s]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s })), [SAVED, browserSet] as const);
		await page.goto('/?set=1-1200');
		const offer = page.getByRole('region', { name: 'Open this shared listening set?' });
		await expect(offer).toContainText('It has 1,200 talkgroups. It replaces the 200 talkgroups in your listening set in this browser.');
		for (const name of ['Replace my listening set', 'Keep mine']) await inViewUnscrolled(page, offer.getByRole('button', { name }));
		await expect(offer.getByRole('listitem').first()).toBeHidden();
		await axe(page, `the share link's offer, ${size.width}px`);
		await shot(page, 'share-offer');
		await offer.getByText('Show the differences').click();
		await expect(offer).toContainText('Only in the link (1,100)');
		await expect(offer).toContainText('Only in your set (100)');
		await expect(offer.getByRole('listitem')).toHaveCount(24);
		await offer.getByRole('button', { name: 'Replace my listening set' }).click();
		await expect(offer).toHaveCount(0);
		expect(await saved(page)).toEqual(range(1, 1200));
	});
