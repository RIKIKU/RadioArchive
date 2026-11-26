/// <reference types="node" />
// The listening set (issue #59; SPEC.md R33, R41, R42, R43, R47, R101; A15;
// ADR 0008): the picker, the share link and fireground links, against the
// binary with the test fixture's setup (scripts/ci/e2e-web.sh): 1001
// "Dispatch North" in group North, 1002 "Fireground 41" with number 41,
// 1003 "Admin" hidden (number 43, also in North) and 1004 ignored (44).
import { test, expect, type Browser, type Page } from '@playwright/test';

const SAVED = 'radioarchive.listening-set';
const SESSION = 'radioarchive.session-additions';

const setList = (page: Page) => page.getByRole('list', { name: 'Your listening set' });
const sessionList = (page: Page) => page.getByRole('list', { name: 'Added for this session' });
const saved = (page: Page) => page.evaluate((k) => localStorage.getItem(k), SAVED);

/** Opens the talkgroups on, which fold away at the page's foot (ADR 0048). */
async function openSet(page: Page) {
	const on = page.locator('details.on-list');
	await expect(on).toBeVisible();
	if ((await on.getAttribute('open')) === null) await on.locator('summary').click();
	await expect(setList(page)).toBeVisible();
}

/** Ticks a talkgroup in the search's results. */
async function pick(page: Page, query: string, name: string) {
	await page.getByLabel('Search talkgroups').fill(query);
	await page.getByRole('checkbox', { name: new RegExp(`^${name}`) }).check();
	await page.getByLabel('Search talkgroups').fill('');
}

/** Collects what the Listen page hands the live feed (the #56 seam). */
async function watchLive(page: Page) {
	await page.addInitScript(() => {
		const w = window as unknown as { liveSets: unknown[] };
		w.liveSets = [];
		window.addEventListener('radioarchive:listening-set', (e) => w.liveSets.push((e as CustomEvent).detail));
	});
	return async () => page.evaluate(() => (window as unknown as { liveSets: unknown[] }).liveSets.at(-1));
}

/** Nothing on the page names hidden 1003 or ignored 1004. */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004', '43', '44']) expect(text, s).not.toContain(s);
}

test('A39: the tiles switch a whole group or tag on and off; All on, All off; the search finds one talkgroup; priority marks', async ({ page }) => {
	const live = await watchLive(page);
	await page.goto('/');
	await expect(page.getByText('Your listening set is empty.')).toBeVisible();
	await expect.poll(live).toEqual([]);
	const summary = page.getByRole('status').filter({ hasText: 'talkgroups on' });
	await expect(summary).toHaveText('0 of 2 talkgroups on');
	await expect(page.getByRole('button', { name: 'All off' })).toBeDisabled();

	// Group North holds only its public talkgroup: one tap switches it on, as it is now.
	const north = page.getByRole('list', { name: 'Agencies' }).getByRole('button', { name: /^North/ });
	const dispatch = page.getByRole('list', { name: 'Categories' }).getByRole('button', { name: /^dispatch/ });
	await expect(north).toHaveAttribute('aria-pressed', 'false');
	await expect(north).toContainText('1 off');
	await north.click();
	await expect(north).toHaveAttribute('aria-pressed', 'true');
	await expect(north).toContainText('All 1 on');
	// The tag's tile names the same talkgroup, so it's on too.
	await expect(dispatch).toHaveAttribute('aria-pressed', 'true');
	await expect(summary).toHaveText('1 of 2 talkgroups on');
	await expect(page.locator('.whats-on')).toContainText(/North\s*all/);
	await expect.poll(live).toEqual([{ id: 1001 }]);
	// All on already: the tap switches it off.
	await north.click();
	await expect(north).toHaveAttribute('aria-pressed', 'false');
	await expect.poll(live).toEqual([]);

	// All on, All off.
	await page.getByRole('button', { name: 'All on' }).click();
	await expect(summary).toHaveText('2 of 2 talkgroups on');
	await expect(page.getByRole('button', { name: 'All on' })).toBeDisabled();
	await expect.poll(live).toEqual([{ id: 1001 }, { id: 1002 }]);
	await page.getByRole('button', { name: 'All off' }).click();
	await expect(summary).toHaveText('0 of 2 talkgroups on');
	await expect.poll(live).toEqual([]);
	// An empty set isn't kept at all.
	expect(await saved(page)).toBeNull();

	await north.click();
	await openSet(page);
	await expect(setList(page).getByRole('listitem')).toHaveText([/Dispatch North/]);

	// Search by label, tag and ID.
	for (const q of ['FG 41', 'fireground', '1002']) {
		await page.getByLabel('Search talkgroups').fill(q);
		await expect(page.getByRole('list', { name: 'Search results' }).getByRole('listitem')).toHaveText([/Fireground 41/]);
	}
	await page.getByLabel('Search talkgroups').fill('admin');
	await expect(page.getByText('No talkgroups match.')).toBeVisible();
	await pick(page, '1002', 'Fireground 41');
	await expect(setList(page).getByRole('listitem')).toHaveText([/Dispatch North/, /Fireground 41/]);
	await expect.poll(live).toEqual([{ id: 1001 }, { id: 1002 }]);

	// Priority, kept in the browser with the set.
	await page.getByRole('checkbox', { name: 'Priority: Fireground 41' }).check();
	expect(await saved(page)).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }, { id: 1002, priority: true }] }));
	await page.reload();
	await openSet(page);
	await expect(page.getByRole('checkbox', { name: 'Priority: Fireground 41' })).toBeChecked();
	await expect(page.getByRole('checkbox', { name: 'Priority: Dispatch North' })).not.toBeChecked();

	// Taking one out.
	await setList(page).getByRole('button', { name: 'Remove Dispatch North' }).click();
	await expect(setList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
	await expect.poll(live).toEqual([{ id: 1002 }]);
	await namesNothingHidden(page);
});

test('A15: a fireground link adds its talkgroup for the session, apart from the saved set', async ({ page, context }) => {
	const live = await watchLive(page);
	await page.goto('/');
	await pick(page, '1001', 'Dispatch North');
	const before = await saved(page);
	expect(before).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] }));

	// Leading zeros don't count.
	await page.goto('/?fireground=041');
	await expect(page.getByRole('status').filter({ hasText: 'Fireground 41 is added for this session.' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Added for this session' })).toBeVisible();
	await expect(sessionList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
	await openSet(page);
	await expect(setList(page).getByRole('listitem')).toHaveText([/Dispatch North/]);
	await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
	// The live feed hears both.
	await expect.poll(live).toEqual([{ id: 1001 }, { id: 1002 }]);
	expect(await saved(page)).toBe(before);

	// The tab closes: the set is as it was, and the addition is gone.
	await page.close();
	const next = await context.newPage();
	await next.goto('/');
	await openSet(next);
	await expect(setList(next).getByRole('listitem')).toHaveText([/Dispatch North/]);
	await expect(next.getByRole('heading', { name: 'Added for this session' })).toHaveCount(0);
	expect(await saved(next)).toBe(before);

	// A talkgroup already in the set is unchanged.
	await next.goto('/?fireground=41');
	await expect(next.getByText('Fireground 41 is added for this session.')).toBeVisible();
	await next.goto('/');
	await pick(next, '1002', 'Fireground 41');
	await openSet(next);
	await next.getByRole('checkbox', { name: 'Priority: Fireground 41' }).check();
	await next.goto('/?fireground=41');
	await expect(next.getByText('Fireground 41 is in your listening set.')).toBeVisible();
	await expect(next.getByRole('heading', { name: 'Added for this session' })).toHaveCount(0);
	await openSet(next);
	await expect(next.getByRole('checkbox', { name: 'Priority: Fireground 41' })).toBeChecked();
});

test('R47: one tap keeps a talkgroup added for the session', async ({ page }) => {
	await page.goto('/?fireground=41');
	await expect(sessionList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
	await page.getByRole('button', { name: 'Keep Fireground 41' }).click();
	await expect(page.getByRole('heading', { name: 'Added for this session' })).toHaveCount(0);
	await openSet(page);
	await expect(setList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
	expect(await saved(page)).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1002 }] }));
	expect(await page.evaluate((k) => sessionStorage.getItem(k), SESSION)).toBeNull();
});

test("A15, R33: an unknown, hidden or ignored number shows one \"This channel isn't available\" and adds nothing", async ({ page }) => {
	await page.goto('/');
	await pick(page, '1001', 'Dispatch North');
	const before = await saved(page);
	const live = await watchLive(page);

	const seen: string[] = [];
	// 99: no talkgroup has it; 43: hidden 1003's; 44: ignored 1004's; and
	// not a number at all.
	for (const n of ['99', '43', '044', '44', 'x41']) {
		await page.goto(`/?fireground=${n}`);
		await expect(page.getByText("This channel isn't available.")).toBeVisible();
		await expect(page.getByRole('heading', { name: 'Find a talkgroup' })).toBeVisible();
		seen.push(await page.locator('main').innerHTML());
		await expect(page.getByRole('heading', { name: 'Added for this session' })).toHaveCount(0);
		await openSet(page);
		await expect(setList(page).getByRole('listitem')).toHaveText([/Dispatch North/]);
		expect(await saved(page)).toBe(before);
		expect(await page.evaluate((k) => sessionStorage.getItem(k), SESSION)).toBeNull();
		await expect.poll(live).toEqual([{ id: 1001 }]);
		await namesNothingHidden(page);
	}
	// The page is the same for each, element for element.
	for (const s of seen) expect(s).toBe(seen[0]);
});

async function listener(browser: Browser, baseURL: string) {
	const context = await browser.newContext({ baseURL });
	return { context, page: await context.newPage() };
}

test('R41: a share link copied from one browser replaces the set in another, after asking', async ({ browser, baseURL }) => {
	// Browser A: {1001, 1002 priority}.
	const a = await listener(browser, baseURL!);
	await a.page.goto('/');
	await pick(a.page, '1001', 'Dispatch North');
	await pick(a.page, '1002', 'Fireground 41');
	await openSet(a.page);
	await a.page.getByRole('checkbox', { name: 'Priority: Fireground 41' }).check();
	const link = await a.page.getByLabel('Share link').inputValue();
	expect(link).toBe(`${new URL(baseURL!).origin}/?set=1001,1002p`);
	await a.context.close();

	// Browser B: {1001}, opens it.
	const b = await listener(browser, baseURL!);
	await b.page.goto('/');
	await pick(b.page, '1001', 'Dispatch North');
	await b.page.goto(link);
	const offer = b.page.getByRole('region', { name: 'Open this shared listening set?' });
	await expect(offer).toContainText('It has 2 talkgroups, 1 priority talkgroup. It replaces the 1 talkgroup in your listening set in this browser.');
	// Nothing changes until it's answered.
	expect(await saved(b.page)).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] }));
	await offer.getByRole('button', { name: 'Replace my listening set' }).click();
	await expect(offer).toHaveCount(0);
	await openSet(b.page);
	await expect(setList(b.page).getByRole('listitem')).toHaveText([/Dispatch North/, /Fireground 41/]);
	await expect(b.page.getByRole('checkbox', { name: 'Priority: Fireground 41' })).toBeChecked();
	expect(await saved(b.page)).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }, { id: 1002, priority: true }] }));
	// Answered once: the address no longer carries it, so a reload asks nothing.
	await expect(b.page).toHaveURL(`${new URL(baseURL!).origin}/`);
	await b.page.reload();
	await expect(b.page.getByRole('heading', { name: 'Your listening set' })).toBeVisible();
	await expect(offer).toHaveCount(0);

	// Its own set again asks nothing.
	await b.page.goto(link);
	await expect(b.page.getByRole('heading', { name: 'Your listening set' })).toBeVisible();
	await expect(offer).toHaveCount(0);

	// Keep mine leaves the set as it was.
	await b.page.goto('/?set=1002');
	await offer.getByRole('button', { name: 'Keep mine' }).click();
	await openSet(b.page);
	await expect(setList(b.page).getByRole('listitem')).toHaveText([/Dispatch North/, /Fireground 41/]);
	await b.context.close();
});

test('R33: a share link naming hidden 1003 opens without it, and without saying so', async ({ browser, baseURL }) => {
	const offerOf = async (query: string) => {
		const c = await listener(browser, baseURL!);
		await c.page.goto(`/?set=${query}`);
		const offer = c.page.getByRole('region', { name: 'Open this shared listening set?' });
		await expect(offer).toBeVisible();
		return { ...c, offer, html: await offer.innerHTML() };
	};
	// 1003 is hidden, 1004 ignored, 9999 never existed: all read alike.
	const hidden = await offerOf('1001,1003p,1002');
	const ignored = await offerOf('1001,1004,1002');
	const unknown = await offerOf('1001,9999p,1002');
	const ranged = await offerOf('1001-1003');
	for (const o of [ignored, unknown, ranged]) expect(o.html).toBe(hidden.html);
	await expect(hidden.offer).toContainText('It has 2 talkgroups.');
	await namesNothingHidden(hidden.page);

	await hidden.offer.getByRole('button', { name: 'Replace my listening set' }).click();
	await openSet(hidden.page);
	await expect(setList(hidden.page).getByRole('listitem')).toHaveText([/Dispatch North/, /Fireground 41/]);
	expect(await saved(hidden.page)).toBe(JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }, { id: 1002 }] }));
	await namesNothingHidden(hidden.page);

	// A link naming only what isn't public asks nothing, as a link naming
	// nothing would.
	for (const q of ['1003', '1003,1004', '9999', '']) {
		const c = await listener(browser, baseURL!);
		await c.page.goto(`/?set=${q}`);
		await expect(c.page.getByText('Your listening set is empty.')).toBeVisible();
		await expect(c.page.getByRole('region', { name: 'Open this shared listening set?' })).toHaveCount(0);
		await c.context.close();
	}
	for (const o of [hidden, ignored, unknown, ranged]) await o.context.close();
});
