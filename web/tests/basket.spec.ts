/// <reference types="node" />
// The download basket's page (issue #81; SPEC.md P1, R45, R46, R64, R65,
// R67, R84, R88; A18; ADR 0015, ADR 0018), against the binary with the
// fixture's made-up archive (testing/webfixture/calls.go): Add to basket
// from a row and the call card, the menu's count, conversations, the
// countdown, an unavailable call, A18's zip from the browser, new since last
// time, a full re-download, Clear, a download in parts, and axe. A19, a call
// hidden since it was added, Add to basket in now playing and Play all are
// basket-live.spec.ts's (@live).
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { readFileSync } from 'node:fs';
import { zipEntry, zipNames } from './zip.ts';

const KEY = 'radioarchive.basket';
const NEVER = '6f1d2c3b-4a59-4e8f-9d7c-0b1a2c3d4e5f';
const NEVER2 = '0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b';

type Call = { id: string; started_at: string; transmissions: { unit: number }[] };
type Kept = { calls: { id: string; added: number }[]; conversations: { id: string; name: string; note?: string; calls: { id: string; added: number }[]; last?: number }[]; last?: number };

/** Public calls from the API: the newest n of a talkgroup that have their recordings (1002 has one in progress). */
async function newest(page: Page, talkgroup: number, n: number): Promise<Call[]> {
	const calls: (Call & { recording: unknown })[] = await page.evaluate(async (t) => (await (await fetch(`/api/v1/recent-calls?talkgroups=${t}&limit=20`)).json()).calls, talkgroup);
	return calls.filter((c) => c.recording).slice(0, n);
}

/** A basket in this browser before the page loads, once (a reload keeps what the page made of it). */
async function withBasket(page: Page, kept: Kept) {
	await page.addInitScript(
		([k, v]) => {
			if (sessionStorage.getItem('test.basket')) return;
			localStorage.setItem(k, v);
			sessionStorage.setItem('test.basket', '1');
		},
		[KEY, JSON.stringify({ v: 1, ...kept })] as const
	);
}

const kept = (page: Page): Promise<Kept> => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? '{"calls":[],"conversations":[]}'), KEY);
const items = (page: Page, list: string) => page.getByRole('list', { name: list }).getByRole('listitem');
const loose = (page: Page) => items(page, 'Calls not in a conversation');

/** Downloads by pressing what's named, and reads the zip. */
async function download(page: Page, name: string | RegExp, within = page.locator('main')): Promise<Buffer> {
	const [d] = await Promise.all([page.waitForEvent('download'), within.getByRole('link', { name }).click()]);
	return readFileSync((await d.path())!);
}
const opus = (zip: Buffer) => zipNames(zip).filter((n) => n.endsWith('.opus'));

/** Nothing on the page names hidden 1003 or ignored 1004. */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004']) expect(text, s).not.toContain(s);
}

test('Add to basket on a recent-calls row and the call card; the menu counts them; Remove takes one out', async ({ page }) => {
	await page.goto('/recent?talkgroups=1001');
	const row = page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem').first();
	const id = (await row.getAttribute('data-call-id'))!;
	const add = row.getByRole('button', { name: /^Add to basket: Dispatch North/ });
	await expect(add).toHaveAttribute('aria-pressed', 'false');
	await add.click();
	await expect(add).toHaveAttribute('aria-pressed', 'true');
	await expect(row.getByRole('status').filter({ hasText: 'Added to the basket' })).toBeVisible();
	// Beside Share, after Download.
	await expect(row.locator('.row-actions > *')).toHaveCount(3);

	const [card] = await newest(page, 1002, 1);
	await page.goto(`/calls/${card.id}`);
	await page.locator('.card').getByRole('button', { name: /^Add to basket: Fireground 41/ }).click();
	await expect(page.locator('.card').getByRole('button', { name: /^Add to basket: Fireground 41/ })).toHaveAttribute('aria-pressed', 'true');
	expect((await kept(page)).calls.map((c) => c.id)).toEqual([id, card.id]);

	// The menu names the basket with how many calls it holds, and the phone's bar has it too.
	const menu = page.getByRole('navigation').getByRole('link', { name: /^Basket/ }).first();
	await expect(menu).toHaveAccessibleName(/2 calls/);
	await menu.click();
	await expect(page).toHaveURL(/\/basket$/);
	await expect(page.getByRole('heading', { level: 1, name: 'Basket' })).toBeVisible();
	await expect(loose(page)).toHaveCount(2);
	await expect(loose(page).first()).toHaveAttribute('data-call-id', id);
	// Each counts down to its deletion by retention (30 days here).
	for (const i of await loose(page).all()) await expect(i.locator('.countdown')).toHaveText(/^deleted in (28|29|30) days$/);
	await expect(loose(page).first().locator('.tg')).toHaveText('Dispatch North');
	await loose(page).first().getByRole('button', { name: /^Remove from the basket: Dispatch North/ }).click();
	await expect(loose(page)).toHaveCount(1);
	expect((await kept(page)).calls.map((c) => c.id)).toEqual([card.id]);
	await page.setViewportSize({ width: 375, height: 812 });
	await expect(page.getByRole('link', { name: /^Basket/ }).last()).toBeVisible();
	await namesNothingHidden(page);
});

test('A18, browser half: 3 calls in "Structure fire" and 1 loose download as A18\'s zip, the same bytes twice', async ({ page }) => {
	await page.goto('/');
	const fire = await newest(page, 1001, 3);
	const [solo] = await newest(page, 1002, 1);
	await withBasket(page, { calls: [...fire, solo].map((c, i) => ({ id: c.id, added: 1_764_000_000_000 + i })), conversations: [] });
	await page.goto('/basket');
	await expect(loose(page)).toHaveCount(4);

	// A conversation, named (the name is required, its error under the field).
	await page.getByRole('button', { name: 'New conversation' }).click();
	await page.getByRole('button', { name: 'Create' }).click();
	await expect(page.locator('#conversation-name-error')).toHaveText('Give the conversation a name.');
	await expect(page.getByLabel('Name required')).toHaveAttribute('aria-invalid', 'true');
	await page.getByLabel('Name required').fill('Structure fire');
	await page.getByLabel('Note').fill('Made-up call-out, 3 crews');
	await page.getByRole('button', { name: 'Create' }).click();
	const conv = page.getByRole('article', { name: 'Structure fire' });
	await expect(conv).toContainText('Made-up call-out, 3 crews');
	for (const c of fire) await page.locator(`.loose [data-call-id="${c.id}"] select`).selectOption({ label: 'Structure fire' });
	await expect(items(page, 'Calls in Structure fire')).toHaveCount(3);
	await expect(loose(page)).toHaveCount(1);
	// In the conversation, by start time.
	const inOrder = [...fire].sort((a, b) => a.started_at.localeCompare(b.started_at)).map((c) => c.id);
	expect(await items(page, 'Calls in Structure fire').evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.callId))).toEqual(inOrder);

	const zip = await download(page, /^Download the basket/);
	const names = zipNames(zip);
	const dir = '01 Structure fire/';
	expect(names.slice(0, 4)).toEqual(['manifest.json', 'index.csv', dir, `${dir}01 Structure fire.m3u`]);
	expect(names.filter((n) => n.startsWith(dir) && n.endsWith('.opus'))).toHaveLength(3);
	expect(names.filter((n) => !n.includes('/') && n.endsWith('.opus'))).toEqual([expect.stringMatching(/_FG 41_1002\.opus$/)]);
	expect(names).toHaveLength(8);
	const manifest = JSON.parse(zipEntry(zip, 'manifest.json').toString());
	expect(manifest.conversations).toEqual([expect.objectContaining({ number: 1, folder: '01 Structure fire', name: 'Structure fire', note: 'Made-up call-out, 3 crews' })]);
	expect(manifest.conversations[0].calls.map((c: { id: string }) => c.id)).toEqual(inOrder);
	expect(manifest.calls.map((c: { id: string }) => c.id)).toEqual([solo.id]);
	const csv = zipEntry(zip, 'index.csv').toString().split('\r\n');
	expect(csv[0]).toBe('call_id,start_utc,end_utc,start_local,end_local,duration_s,system,talkgroup_id,talkgroup_name,talkgroup_label,groups,tag,unit_ids,unit_aliases,unit_offsets_s,file');
	expect(csv.filter(Boolean)).toHaveLength(5);

	// The basket stays, and the same download again is the same bytes (R65).
	await expect(conv).toBeVisible();
	await expect(page.getByText(/^Last downloaded/).first()).toBeVisible();
	const again = await download(page, /^Download the basket again/, page.locator('.whole'));
	expect(again.equals(zip)).toBe(true);
	// The conversation on its own: one folder.
	const one = await download(page, /^Download Structure fire again/, conv);
	expect(zipNames(one).filter((n) => n.endsWith('.opus'))).toHaveLength(3);
	expect(JSON.parse(zipEntry(one, 'manifest.json').toString()).calls).toEqual([]);
});

test('Download new since last time takes only the calls added since; a full re-download takes them all; Clear empties the basket', async ({ page }) => {
	await page.goto('/');
	const [a, b, c] = await newest(page, 1001, 3);
	await withBasket(page, { calls: [a, b].map((x, i) => ({ id: x.id, added: 1_764_000_000_000 + i })), conversations: [] });
	await page.goto('/basket');
	const whole = page.locator('.whole');
	await expect(whole.getByRole('link', { name: /^Download new since last time/ })).toHaveCount(0);
	expect(opus(await download(page, /^Download the basket \(2 calls\)/, whole))).toHaveLength(2);
	await expect(whole.getByText('Nothing new since last time.')).toBeVisible();

	// One more, from Recent calls.
	await page.goto('/recent?talkgroups=1001');
	await page.locator(`[data-call-id="${c.id}"]`).getByRole('button', { name: /^Add to basket/ }).click();
	await page.goto('/basket');
	const fresh = await download(page, /^Download new since last time \(1 call\)/, whole);
	expect(opus(fresh)).toHaveLength(1);
	expect(JSON.parse(zipEntry(fresh, 'manifest.json').toString()).calls.map((x: { id: string }) => x.id)).toEqual([c.id]);
	expect(opus(await download(page, /^Download the basket again \(3 calls\)/, whole))).toHaveLength(3);

	// Clear, confirmed.
	await whole.getByRole('button', { name: 'Clear', exact: true }).click();
	await whole.getByRole('button', { name: 'Clear the basket' }).click();
	await expect(page.getByRole('heading', { name: 'Your basket is empty' })).toBeVisible();
	expect(await page.evaluate((k) => localStorage.getItem(k), KEY)).toBeNull();
});

/** Made-up notes long enough that three conversations take two zips. */
const longNote = (n: number) => `Made-up incident ${n}. ${'Crews worked the exposure from both sides while command moved twice. '.repeat(28)}`.slice(0, 1990);

/** A basket of three conversations with long notes, an unavailable call and a loose call: two parts. */
async function bigBasket(page: Page) {
	await page.goto('/');
	const calls = await newest(page, 1001, 7);
	const [fg] = await newest(page, 1002, 1);
	const all = [...calls.map((c) => c.id), fg.id, NEVER, NEVER2];
	const added = (i: number) => 1_764_000_000_000 + i;
	await withBasket(page, {
		calls: all.map((id, i) => ({ id, added: added(i) })),
		conversations: [
			{ id: 'c1', name: 'Structure fire, Example Street', note: longNote(1), calls: [calls[0], calls[1], calls[2]].map((c, i) => ({ id: c.id, added: added(10 + i) })) },
			{ id: 'c2', name: 'Grass fire', note: longNote(2), calls: [calls[3], calls[4]].map((c, i) => ({ id: c.id, added: added(20 + i) })) },
			{ id: 'c3', name: 'Car accident', note: longNote(3), calls: [{ id: calls[5].id, added: added(30) }, { id: NEVER, added: added(31) }] }
		]
	});
	return { calls, fg };
}

test('a basket too big for one URL downloads in parts, each a zip; an unavailable call is left out (R65, R72)', async ({ page }) => {
	const { calls, fg } = await bigBasket(page);
	await page.goto('/basket');
	const whole = page.locator('.whole');
	const parts = whole.getByRole('button', { name: /^Download the basket \(8 calls, 2 parts\)/ });
	await expect(parts).toHaveAttribute('aria-expanded', 'false');
	// One conversation fits in one zip.
	await expect(page.getByRole('article', { name: 'Grass fire' }).getByRole('link', { name: /^Download Grass fire \(2 calls\)/ })).toBeVisible();
	await parts.click();
	await expect(parts).toHaveAttribute('aria-expanded', 'true');
	const list = page.getByRole('region', { name: 'the basket, in parts' });
	await expect(list.getByRole('link')).toHaveText([/^Part 1 of 2/, /^Part 2 of 2/]);
	const got: string[] = [];
	for (const n of [1, 2]) {
		const zip = await download(page, new RegExp(`^Part ${n} of 2`), list);
		const m = JSON.parse(zipEntry(zip, 'manifest.json').toString());
		expect(m.part).toBe(n);
		expect(m.parts).toBe(2);
		got.push(...m.calls.map((c: { id: string }) => c.id), ...m.conversations.flatMap((v: { calls: { id: string }[] }) => v.calls.map((c) => c.id)));
	}
	// Every available call is in one part or another, the unavailable one in none.
	expect(new Set(got)).toEqual(new Set([...calls.map((c) => c.id), fg.id]));
	expect(got).not.toContain(NEVER);
	// Downloaded: the whole basket's is each conversation's last download too.
	await expect(page.getByRole('article', { name: 'Grass fire' }).getByRole('link', { name: /^Download Grass fire again/ })).toBeVisible();
});

test("an unavailable call shows only \"No longer available\", the same for every call that's gone, naming nothing", async ({ page }) => {
	await bigBasket(page);
	await page.goto('/basket');
	// One in "Car accident", one loose.
	const gone = page.locator('[data-state="unavailable"]');
	await expect(gone).toHaveCount(2);
	for (const g of await gone.all()) {
		await expect(g).toHaveText(/^No longer available\s*(Take out|Remove)$/);
		await expect(g.locator('time, .tg, .countdown')).toHaveCount(0);
	}
	await expect(page.locator('.summary')).toContainText('2 calls no longer available.');
	await namesNothingHidden(page);
	// Taken out of the conversation, it stays in the basket; removed, it's gone.
	await page.getByRole('article', { name: 'Car accident' }).getByRole('button', { name: 'Take out of Car accident: a call no longer available' }).click();
	await expect(gone).toHaveCount(2);
	await expect(page.locator('.loose [data-state="unavailable"]')).toHaveCount(2);
	await loose(page).getByRole('button', { name: 'Remove from the basket: a call no longer available' }).first().click();
	await expect(gone).toHaveCount(1);
});

test('axe: the basket with conversations, countdowns, an unavailable call and a download in parts, in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await bigBasket(page);
	await page.goto('/basket');
	await page.locator('.whole').getByRole('button', { name: /^Download the basket/ }).click();
	await expect(page.getByRole('region', { name: 'the basket, in parts' })).toBeVisible();
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
				await expect(page.locator('.countdown').first()).toHaveText(/^deleted in \d+ days$/);
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
				expect(results.violations, `${look} ${theme}, ${width} px`).toEqual([]);
				expect(await page.evaluate(() => document.documentElement.scrollWidth), `${width} px scrolls sideways`).toBeLessThanOrEqual(width);
				if (shots) {
					await page.mouse.move(0, 0);
					await page.screenshot({ path: `${shots}/basket-${look}-${theme}-${width}.png`, fullPage: true });
				}
			}
	}
	// The empty basket too.
	await page.evaluate((k) => localStorage.removeItem(k), KEY);
	await page.reload();
	await expect(page.getByRole('heading', { name: 'Your basket is empty' })).toBeVisible();
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
});
