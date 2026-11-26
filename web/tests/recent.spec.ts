/// <reference types="node" />
// The Recent calls page (issue #79; SPEC.md R24, R42, R46, R62, R63, R73,
// R80, R84, section 13; A17, A22; ADR 0013, ADR 0016), against the binary
// with the test fixture's setup and the made-up archive the fixture's
// command stores (testing/webfixture/calls.go; scripts/ci/e2e-web.sh): 61
// public calls on 1001 Dispatch North (one of them A17's, unit 2099, 26
// hours ago), 9 on 1002 Fireground 41 (one in progress), and calls on 1003
// (hidden), patched with 1003 and on 1004 (ignored), which must never show.
// The API runs in TZ=Australia/Melbourne; the browsers in UTC.
//
// The tests that change a talkgroup, or replay a call with live listening
// on, are recent-live.spec.ts's (@live, one at a time).
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { readFileSync } from 'node:fs';

const ZONE = 'Australia/Melbourne';
const SAVED = 'radioarchive.listening-set';

const list = (page: Page) => page.getByRole('list', { name: 'Recent calls' });
const rows = (page: Page) => list(page).getByRole('listitem');
const ids = (page: Page) => rows(page).evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.callId));

/** Opens a page with a listening set ({1001} if none given). */
async function withSet(page: Page, set: { id: number }[] = [{ id: 1001 }]) {
	await page.addInitScript(([k, s]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s })), [SAVED, set] as const);
}

/** The API's own answer for a search, every page of it. */
async function apiIDs(page: Page, query: string): Promise<string[]> {
	return page.evaluate(async (q) => {
		const out: string[] = [];
		let cursor = '';
		for (;;) {
			const r = await fetch(`/api/v1/recent-calls?${q}&limit=100${cursor ? `&cursor=${cursor}` : ''}`);
			const b = await r.json();
			out.push(...b.calls.map((c: { id: string }) => c.id));
			if (!b.next) return out;
			cursor = b.next;
		}
	}, query);
}

/** The one call a unit was heard on, as the API gives it. */
async function callOf(page: Page, unit: number) {
	return page.evaluate(async (u) => (await (await fetch(`/api/v1/recent-calls?unit=${u}`)).json()).calls[0], unit);
}

/** Nothing on the page names hidden 1003 or ignored 1004 (a unit the listener typed is theirs to see). */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004']) expect(text, s).not.toContain(s);
}

const openFilters = async (page: Page) => {
	const toggle = page.getByRole('button', { name: 'Filters' });
	if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
};

test('the list: the listening set newest first, its rows, and Load more', async ({ page }) => {
	await withSet(page);
	await page.goto('/recent');
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
	await expect(rows(page)).toHaveCount(50);
	await expect(page.getByText(/Your listening set \(1 talkgroup\)/)).toBeVisible();
	const want = await apiIDs(page, 'talkgroups=1001');
	expect(want.length).toBe(61);
	expect(await ids(page)).toEqual(want.slice(0, 50));
	// Each row: the name (R24), the group, the start in TZ, the duration and the units.
	const first = rows(page).first();
	await expect(first).toContainText('Dispatch North');
	await expect(first).toContainText('North');
	await expect(first.getByRole('link', { name: /^Download: Dispatch North/ })).toBeVisible();
	await page.getByRole('button', { name: 'Load more' }).click();
	await expect(rows(page)).toHaveCount(61);
	expect(await ids(page)).toEqual(want);
	await expect(page.getByText("That's every call that matches.")).toBeVisible();
	await expect(page.getByRole('button', { name: 'Load more' })).toHaveCount(0);
	await namesNothingHidden(page);
});

test("times are the deployment's (TZ), not the browser's", async ({ page }) => {
	await withSet(page);
	await page.goto('/recent?unit=2099');
	await expect(rows(page)).toHaveCount(1);
	const c = await callOf(page, 2099);
	const local = new Intl.DateTimeFormat('en-AU', { timeZone: ZONE, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(c.started_at));
	await expect(rows(page).first().locator('time')).toContainText(local);
	expect(local).toMatch(/:07$/);
	await expect(page.getByText('Times are Melbourne time.')).toBeVisible();
});

test('filters: talkgroups picked with Suggest, a unit, the order, kept in the address', async ({ page }) => {
	await withSet(page);
	await page.goto('/recent');
	await expect(rows(page)).toHaveCount(50);
	await openFilters(page);
	await page.getByRole('radio', { name: 'These talkgroups' }).check();
	await page.getByRole('combobox', { name: 'Add a talkgroup' }).fill('fire');
	await page.getByRole('option', { name: /Fireground 41/ }).click();
	await expect(page.getByRole('list', { name: 'Chosen talkgroups' })).toContainText('Fireground 41');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(/\/recent\?talkgroups=1002$/);
	await expect(rows(page)).toHaveCount(9);
	for (const t of await rows(page).locator('.tg').allInnerTexts()) expect(t).toBe('Fireground 41');
	// The call in progress has no recording yet: no Download, and Listen live.
	const live = rows(page).filter({ hasText: 'In progress' });
	await expect(live).toHaveCount(1);
	await expect(live.getByRole('link', { name: /^Download/ })).toHaveCount(0);
	await expect(live.getByRole('button', { name: /^Listen live/ })).toBeVisible();

	// A unit, by radio ID, and oldest first.
	await page.getByRole('radio', { name: 'Your listening set' }).check();
	await page.getByLabel('Unit', { exact: true }).fill('2010');
	await page.getByLabel('Order').selectOption('oldest');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(/\/recent\?unit=2010&order=oldest$/);
	const want = await apiIDs(page, 'talkgroups=1001&unit=2010&order=oldest');
	expect(want.length).toBe(12);
	await expect(rows(page)).toHaveCount(12);
	expect(await ids(page)).toEqual(want);

	// The address alone opens the same list: a link to it works.
	await page.goto('/recent?unit=2010&order=oldest');
	await expect(rows(page)).toHaveCount(12);
	expect(await ids(page)).toEqual(want);

	// A unit neither picked from the list nor a radio ID is the form's to
	// say, under the field.
	await openFilters(page);
	await page.getByLabel('Unit', { exact: true }).fill('Nobody at all');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page.locator('#unit-error')).toHaveText('Choose a unit from the list, or enter a radio ID in digits, such as 2001.');
	await expect(page.getByLabel('Unit', { exact: true })).toBeFocused();
	await expect(page.getByText('1 field needs attention')).toBeVisible();

	// Clear filters: the listening set again.
	await page.getByRole('button', { name: 'Clear filters' }).click();
	await expect(page).toHaveURL(/\/recent$/);
	await expect(rows(page)).toHaveCount(50);
});

test('a date and time range, picked in the calendar on the deployment clock', async ({ page }) => {
	await withSet(page);
	await page.goto('/recent');
	const c = await callOf(page, 2099);
	const at = new Date(c.started_at);
	const parts = Object.fromEntries(
		new Intl.DateTimeFormat('en-GB', { timeZone: ZONE, year: 'numeric', month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
			.formatToParts(at)
			.map((p) => [p.type, p.value])
	);
	const hhmm = `${parts.hour}:${parts.minute}`;
	await openFilters(page);
	await page.getByRole('button', { name: 'Dates' }).click();
	const calendar = page.getByRole('dialog', { name: 'Choose dates' });
	const day = calendar.getByRole('gridcell', { name: new RegExp(`\\b${parts.day} ${parts.month} ${parts.year}$`) });
	if (!(await day.isVisible())) await calendar.getByRole('button', { name: /previous/i }).first().click();
	await day.click();
	await day.click();
	await calendar.getByLabel('From time').fill(hhmm);
	await calendar.getByLabel('To time').fill(hhmm);
	await calendar.getByRole('button', { name: 'Done' }).click();
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(new RegExp(`from=\\d{4}-\\d{2}-\\d{2}&from_time=${hhmm}&to_time=${hhmm}`));
	// A minute of the listening set: A17's call alone.
	await expect(rows(page)).toHaveCount(1);
	expect(await ids(page)).toEqual([c.id]);
});

test('a tap on a time opens the browser\'s time picker; the keyboard still types it', async ({ page }) => {
	// ArchiveUx 0.5.1: a pointer's click calls showPicker(); focus and keys don't.
	// The browser's own picker can't be seen from a test, so showPicker counts.
	await page.addInitScript(() => {
		const w = window as unknown as { pickers: string[] };
		w.pickers = [];
		HTMLInputElement.prototype.showPicker = function (this: HTMLInputElement) {
			w.pickers.push(this.labels?.[0]?.textContent ?? '');
		};
	});
	const pickers = () => page.evaluate(() => (window as unknown as { pickers: string[] }).pickers);
	await withSet(page);
	await page.goto('/recent');
	await openFilters(page);
	await page.getByRole('button', { name: 'Dates' }).click();
	let calendar = page.getByRole('dialog', { name: 'Choose dates' });
	// No day yet: the times are disabled, and a tap opens nothing.
	await expect(calendar.getByLabel('From time')).toBeDisabled();
	await calendar.getByLabel('From time').click({ force: true });
	expect(await pickers()).toEqual([]);
	await calendar.getByRole('button', { name: 'Done' }).click();

	await page.goto('/recent?from=2026-11-20');
	await openFilters(page);
	await page.getByRole('button', { name: /^Dates/ }).click();
	calendar = page.getByRole('dialog', { name: 'Choose dates' });
	await calendar.getByLabel('From time').click();
	await calendar.getByLabel('To time').click();
	expect(await pickers()).toEqual(['From time', 'To time']);
	// From the keyboard: focus and Enter open nothing, and Enter still closes the calendar.
	// (Tab moves between a time's own segments in some browsers, so the test focuses each.)
	await calendar.getByLabel('From time').focus();
	await calendar.getByLabel('To time').focus();
	await expect(calendar.getByLabel('To time')).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(calendar).toBeHidden();
	expect(await pickers()).toEqual(['From time', 'To time']);
});

test('A22, browser half: a 32-day range shows the error naming the 31-day limit under the form', async ({ page }) => {
	await withSet(page);
	await page.goto('/recent?from=2026-10-01&to=2026-11-01');
	const error = page.locator('#dates-error');
	await expect(error).toHaveText('Search at most 31 days at once. Try a narrower time range.');
	await expect(error).toBeVisible();
	await expect(page.getByRole('group', { name: 'Date and time' })).toHaveAttribute('aria-describedby', /dates-error/);
	await expect(rows(page)).toHaveCount(0);
	// 31 days is a search.
	await page.goto('/recent?from=2026-10-01&to=2026-10-31');
	await expect(page.locator('#dates-error')).toHaveCount(0);
	await expect(page.getByText(/No calls match|That's every call/)).toBeVisible();
});

test('no page shows 1003, "Admin" or its label, whatever the filters', async ({ page }) => {
	await withSet(page, [{ id: 1001 }, { id: 1003 }, { id: 1004 }]);
	for (const q of ['', '?talkgroups=all', '?talkgroups=1003', '?talkgroups=1001,1003', '?talkgroups=1-9999', '?unit=9003', '?unit=9004', '?unit=9005', '?talkgroups=all&order=oldest']) {
		await page.goto(`/recent${q}`);
		await expect(page.getByText(/Loading/)).toHaveCount(0);
		await openFilters(page);
		await namesNothingHidden(page);
		// Units heard only on hidden or ignored calls list nothing.
		if (q.startsWith('?unit=')) await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
	}
	// A filter naming only hidden talkgroups lists nothing, as one naming none that exist.
	await page.goto('/recent?talkgroups=1003');
	await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
	const hidden = await page.locator('main').innerHTML();
	await page.goto('/recent?talkgroups=4711');
	await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
	expect(await page.locator('main').innerHTML()).toBe(hidden);
});

test('phones: a row is Play, the call and More actions on one line; More actions opens the other four under it; desktops show them in the row (#108)', async ({ page }) => {
	await withSet(page);
	await page.setViewportSize({ width: 375, height: 812 });
	await page.goto('/recent');
	await expect(rows(page)).toHaveCount(50);
	const [first, second] = [rows(page).nth(0), rows(page).nth(1)];
	const named = (row: typeof first) => ({
		play: row.getByRole('button', { name: /^Play: Dispatch North/ }),
		more: row.getByRole('button', { name: /^More actions: Dispatch North, / }),
		fromHere: row.getByRole('button', { name: /^Play from here: Dispatch North/ }),
		download: row.getByRole('link', { name: /^Download: Dispatch North/ }),
		share: row.getByRole('button', { name: /^Share: Dispatch North/ }),
		add: row.getByRole('button', { name: /^Add to basket: Dispatch North/ })
	});
	const a = named(first);
	const b = named(second);
	const four = (x: typeof a) => [x.fromHere, x.download, x.share, x.add];

	// Closed: Play and More actions only, on the call's line, each a whole target.
	await expect(a.more).toHaveAttribute('aria-expanded', 'false');
	for (const x of four(a)) await expect(x).toBeHidden();
	const box = async (l: typeof first) => (await l.boundingBox())!;
	const [play, what, more] = [await box(a.play), await box(first.locator('.what')), await box(a.more)];
	for (const [n, x] of [['Play', play], ['More actions', more]] as const) {
		expect(x.width, `${n} is a whole target`).toBeGreaterThanOrEqual(44);
		expect(x.height, `${n} is a whole target`).toBeGreaterThanOrEqual(44);
		expect(x.y, `${n} is on the call's line`).toBeLessThan(what.y + what.height);
		expect(x.y + x.height, `${n} is on the call's line`).toBeGreaterThan(what.y);
	}
	expect(what.x).toBeGreaterThan(play.x + play.width);
	expect(more.x).toBeGreaterThanOrEqual(what.x + what.width);

	// Open: the four, in order, under the call; it stays open while one says what it did.
	await a.more.click();
	await expect(a.more).toHaveAttribute('aria-expanded', 'true');
	for (const x of four(a)) await expect(x).toBeVisible();
	expect((await box(a.fromHere)).y).toBeGreaterThanOrEqual(what.y + what.height);
	await expect(first.locator('.actions > *')).toHaveCount(4);
	await a.add.click();
	await expect(first.getByRole('status').filter({ hasText: 'Added to the basket' })).toBeVisible();
	await expect(a.share).toBeVisible();
	// One open at a time.
	await b.more.click();
	await expect(b.more).toHaveAttribute('aria-expanded', 'true');
	await expect(a.more).toHaveAttribute('aria-expanded', 'false');
	await expect(a.share).toBeHidden();
	// Escape closes it, and focus inside it goes back to its button.
	await b.share.focus();
	await page.keyboard.press('Escape');
	await expect(b.more).toHaveAttribute('aria-expanded', 'false');
	await expect(b.more).toBeFocused();
	await expect(b.share).toBeHidden();

	// Desktops: no More actions, the four in the row.
	await page.setViewportSize({ width: 1280, height: 900 });
	await expect(a.more).toBeHidden();
	for (const x of four(a)) await expect(x).toBeVisible();
	await expect(a.add).toHaveAttribute('aria-pressed', 'true');
});

test("A17, browser half: Download on a row saves the stored file under R63's name", async ({ page }) => {
	await withSet(page);
	await page.goto('/recent?unit=2099');
	await expect(rows(page)).toHaveCount(1);
	const c = await callOf(page, 2099);
	const p = Object.fromEntries(
		new Intl.DateTimeFormat('en-GB', { timeZone: ZONE, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })
			.formatToParts(new Date(c.started_at))
			.map((x) => [x.type, x.value])
	);
	const name = `${p.year}-${p.month}-${p.day}_${p.hour}${p.minute}${p.second}_DISP N_1001.opus`;
	const [download] = await Promise.all([page.waitForEvent('download'), rows(page).first().getByRole('link', { name: /^Download/ }).click()]);
	// The name the server gives, exactly; Playwright's WebKit build on Linux
	// saves spaces in a download's name as underscores, whatever the name.
	expect(download.suggestedFilename()).toBe(test.info().project.name === 'webkit' ? name.replaceAll(' ', '_') : name);
	const disposition = await page.evaluate(async (u) => (await fetch(u, { method: 'HEAD' })).headers.get('Content-Disposition'), `/api/v1/calls/${c.id}/download`);
	expect(disposition).toBe(`attachment; filename="${name}"`);
	const saved = readFileSync((await download.path())!);
	const stored = Buffer.from(await page.evaluate(async (u) => Array.from(new Uint8Array(await (await fetch(u)).arrayBuffer())), c.recording.url));
	expect(saved.equals(stored)).toBe(true);
	expect(saved.subarray(0, 4).toString()).toBe('OggS');
});

test('Recent calls is in the menu, and Recent in the phone bar', async ({ page }) => {
	await page.goto('/');
	await page.getByRole('navigation').getByRole('link', { name: 'Recent calls' }).first().click();
	await expect(page).toHaveURL(/\/recent$/);
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
	await page.setViewportSize({ width: 375, height: 812 });
	await expect(page.getByRole('link', { name: 'Recent', exact: true })).toBeVisible();
});

test('axe: the list, the filters open, an error and an empty list, in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await withSet(page, [{ id: 1001 }, { id: 1002 }]);
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
						await page.screenshot({ path: `${shots}/recent-${state}-${look}-${theme}-${width}.png` });
					}
				}
		}
	};
	await page.goto('/recent');
	await check('list', () => expect(rows(page)).toHaveCount(50));
	// A phone's More actions open on the first row with a recording (on a desktop the row is as before).
	const recorded = rows(page).filter({ has: page.locator('.from-here') }).first();
	await recorded.getByRole('button', { name: /^More actions: / }).click();
	await check('actions-open', () => expect(recorded.getByRole('button', { name: /^Play from here: / })).toBeVisible());
	await page.keyboard.press('Escape');
	await openFilters(page);
	await page.getByRole('radio', { name: 'These talkgroups' }).check();
	await page.getByRole('combobox', { name: 'Add a talkgroup' }).fill('d');
	await page.getByRole('option', { name: /Dispatch North/ }).click();
	await page.getByLabel('Unit', { exact: true }).fill('2001');
	await check('filters', () => expect(page.getByRole('list', { name: 'Chosen talkgroups' })).toBeVisible());
	await page.goto('/recent?from=2026-10-01&to=2026-11-01');
	await check('error', () => expect(page.locator('#dates-error')).toBeVisible());
	await page.goto('/recent?unit=4242');
	await check('empty', () => expect(page.getByText(/No calls match/)).toBeVisible());
});
