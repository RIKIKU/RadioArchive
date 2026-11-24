/// <reference types="node" />
// A shared conversation with calls of its own (issue #82; SPEC.md P1, R33,
// R66; ADR 0020), stored with the fixture's command (RA_WEBFIXTURE): a
// hidden call in it is dropped as a deleted one is, a conversation of
// hidden calls is unavailable as one of deleted calls is, and Play all
// plays the calls in order. scripts/ci/e2e-web.sh runs these with the
// playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { dock, env } from './live.ts';
import { conversationPath } from '../src/lib/share/conversation.ts';

const NAME = 'Structure fire';
const NOTE = 'Pumper 1 first on scene.';

function fixture(...args: string[]): Promise<string> {
	return new Promise((resolve, reject) =>
		execFile(env('RA_WEBFIXTURE'), [env('RA_DATABASE_URL'), ...args], { timeout: 30_000 }, (err, out, stderr) =>
			err ? reject(new Error(`${err.message}\n${stderr}`)) : resolve(out.trim())
		)
	);
}

/** A call of 4 s on talkgroup from unit, started age ago, with its recording. */
const dated = (talkgroup: number, unit: number, age: string) => fixture('call-dated', 'Bench', env('RA_RECORDINGS'), String(talkgroup), String(unit), age);

const cards = (page: Page) => page.locator('.cards > li > article');

/** The page a link opens, once it has settled: main's markup. */
async function opened(page: Page, link: string, settled: () => Promise<void>): Promise<string> {
	await page.goto('/');
	await page.goto(link);
	await settled();
	return page.locator('main').innerHTML();
}

/** Nothing on the page names hidden 1003 or ignored 1004, or their units. */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004', '2653', '2654']) expect(text, s).not.toContain(s);
}

test('@live P1, R66: a hidden or ignored call in a conversation is dropped as a deleted one is; with every call hidden, as with every call deleted, it isn\'t available', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	const a = await dated(1001, 2651, '3m');
	const b = await dated(1002, 2652, '1m');
	// 1003 is hidden and 1004 ignored in the fixture's setup.
	const hidden = await dated(1003, 2653, '2m');
	const ignored = await dated(1004, 2654, '2m');
	const deleted = await dated(1002, 2655, '2m');
	const deleted2 = await dated(1002, 2656, '2m');
	await fixture('call-delete', deleted, env('RA_RECORDINGS'));
	await fixture('call-delete', deleted2, env('RA_RECORDINGS'));
	const link = (ids: string[]) => conversationPath({ name: NAME, note: NOTE, calls: ids });

	const two = () => expect(cards(page)).toHaveCount(2);
	const withDeleted = await opened(page, link([a, deleted, b]), two);
	expect(await cards(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-call-id')))).toEqual([a, b]);
	await expect(page.locator('.head .meta')).toContainText('2 calls');
	for (const other of [hidden, ignored]) {
		expect(await opened(page, link([a, other, b]), two)).toBe(withDeleted);
		await namesNothingHidden(page);
	}

	const none = () => expect(page.getByRole('heading', { level: 1, name: "This conversation isn't available" })).toBeVisible();
	const allDeleted = await opened(page, link([deleted, deleted2]), none);
	for (const ids of [[hidden], [hidden, ignored], [ignored, deleted]]) {
		expect(await opened(page, link(ids), none)).toBe(allDeleted);
		await namesNothingHidden(page);
	}
	expect(await page.locator('body').innerText()).not.toContain(NAME);
});

test('@live R66: Play all plays a shared conversation\'s calls in its order in the dock', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	const a = await dated(1001, 2661, '3m');
	const b = await dated(1002, 2662, '2m');
	const c = await dated(1001, 2663, '1m');
	await page.addInitScript(() => {
		const order: string[] = [];
		(window as unknown as { order: string[] }).order = order;
		addEventListener('radioarchive:first-frame', (e) => order.push((e as CustomEvent).detail.call_id));
	});
	await page.goto(conversationPath({ name: NAME, note: NOTE, calls: [a, b, c] }));
	await expect(page.locator('.cards > li > article[data-state="recorded"]')).toHaveCount(3);
	await page.getByRole('button', { name: 'Play all' }).click();
	await expect(dock(page).locator('.replay')).toContainText(`${NAME}: call 1 of 3`);
	await expect(dock(page).locator('.replay')).toContainText(`${NAME}: call 3 of 3`, { timeout: 20_000 });
	await expect(dock(page).locator('.replay')).toHaveCount(0, { timeout: 15_000 });
	expect(await page.evaluate(() => (window as unknown as { order: string[] }).order)).toEqual([a, b, c]);
});
