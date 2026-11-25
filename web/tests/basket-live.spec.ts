/// <reference types="node" />
// The download basket with the whole stack (issue #81; SPEC.md P1, R45,
// R64, R88; A19; ADR 0005, ADR 0018): retention dating and deleting a
// basket's call (A19), a talkgroup hidden after its call was added, Add to
// basket in now playing for a live call, and a conversation played in
// order, with the fixture's command (RA_WEBFIXTURE) and the fake recorder's
// calls through the recorder client. scripts/ci/e2e-web.sh runs these with
// the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { dock, env, firstOf, listen, seconds, sendCalls, setTalkgroup } from './live.ts';
import { zipEntry, zipNames } from './zip.ts';

const KEY = 'radioarchive.basket';

/** The fixture's command: on the database, or, for what an admin does (the retention period), on the API (ADR 0025). */
function fixture(...args: string[]): Promise<string> {
	const target = args[0] === 'retention' ? env('RA_URL') : env('RA_DATABASE_URL');
	return new Promise((resolve, reject) =>
		execFile(env('RA_WEBFIXTURE'), [target, ...args], { timeout: 30_000 }, (err, out, stderr) =>
			err ? reject(new Error(`${err.message}\n${stderr}`)) : resolve(out.trim())
		)
	);
}

/** A basket in this browser before the page loads, once. */
async function withBasket(page: Page, ids: string[], conversations: { id: string; name: string; calls: string[] }[] = []) {
	const kept = {
		v: 1,
		calls: ids.map((id, i) => ({ id, added: 1_764_000_000_000 + i })),
		conversations: conversations.map((c) => ({ ...c, calls: c.calls.map((id, i) => ({ id, added: 1_764_000_000_100 + i })) }))
	};
	await page.addInitScript(
		([k, v]) => {
			if (sessionStorage.getItem('test.basket')) return;
			localStorage.setItem(k, v);
			sessionStorage.setItem('test.basket', '1');
		},
		[KEY, JSON.stringify(kept)] as const
	);
}

const item = (page: Page, id: string) => page.locator(`.loose [data-call-id="${id}"]`);

/** The basket's whole download, as the zip's manifest lists it. */
async function downloadWhole(page: Page): Promise<string[]> {
	const [d] = await Promise.all([page.waitForEvent('download'), page.locator('.whole').getByRole('link', { name: /^Download the basket/ }).click()]);
	const zip = readFileSync((await d.path())!);
	expect(zipNames(zip)[0]).toBe('manifest.json');
	const m = JSON.parse(zipEntry(zip, 'manifest.json').toString());
	return [...m.calls.map((c: { id: string }) => c.id), ...m.conversations.flatMap((v: { calls: { id: string }[] }) => v.calls.map((c) => c.id))];
}

test('@live A19: a call recorded 29 days ago with retention 30 days shows "deleted in 1 day"; after the nightly run on day 31 it\'s no longer available and left out of the zip', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await fixture('retention', '30');
	const old = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1001', '2601', '696h');
	const fresh = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1002', '2602', '1h');
	await withBasket(page, [old, fresh]);
	await page.goto('/basket');
	await expect(item(page, old).locator('.countdown')).toHaveText('deleted in 1 day');
	await expect(item(page, old).locator('.tg')).toHaveText('Dispatch North');
	await expect(item(page, fresh).locator('.countdown')).toHaveText(/^deleted in (29|30) days$/);
	expect(new Set(await downloadWhole(page))).toEqual(new Set([old, fresh]));

	// Day 31's nightly run deletes it.
	expect(Number(await fixture('nightly', env('RA_RECORDINGS'), '48h'))).toBeGreaterThanOrEqual(1);
	await page.reload();
	await expect(item(page, old)).toHaveAttribute('data-state', 'unavailable');
	await expect(item(page, old)).toHaveText(/^No longer available/);
	await expect(item(page, fresh)).toHaveAttribute('data-state', 'recorded');
	// Left out of the zip: the whole basket's is the fresh call's alone.
	await expect(page.locator('.whole').getByRole('link', { name: /^Download the basket again \(1 call\)/ })).toBeVisible();
	expect(await downloadWhole(page)).toEqual([fresh]);
});

test('@live P1: a call on 1001 added and then 1001 hidden shows "No longer available" as a deleted call does, and nothing names 1001', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	const hiddenSoon = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1001', '2611', '1h');
	const deleted = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1002', '2612', '1h');
	await withBasket(page, [hiddenSoon, deleted]);
	await page.goto('/basket');
	await expect(item(page, hiddenSoon).locator('.tg')).toHaveText('Dispatch North');
	try {
		await fixture('call-delete', deleted, env('RA_RECORDINGS'));
		await setTalkgroup(1001, 'state', 'hidden');
		await page.reload();
		await expect(item(page, hiddenSoon)).toHaveAttribute('data-state', 'unavailable');
		await expect(item(page, deleted)).toHaveAttribute('data-state', 'unavailable');
		// The same markup, but for the call's own ID.
		const markup = async (id: string) => (await item(page, id).evaluate((e) => e.outerHTML)).replaceAll(id, '<id>');
		expect(await markup(hiddenSoon)).toBe(await markup(deleted));
		const text = await page.locator('body').innerText();
		for (const s of ['Dispatch North', 'DISP N', '1001', 'North']) expect(text, s).not.toContain(s);
		await expect(page.locator('.whole').getByText('Nothing to download yet.')).toBeVisible();
	} finally {
		await setTalkgroup(1001, 'state', 'public');
	}
});

test('@live R45: Add to basket in now playing takes the live call at once; it can be downloaded once recorded', async ({ page }) => {
	test.setTimeout(3 * 60_000);
	await listen(page);
	const sending = sendCalls([{ unit: 2621, atMs: 200, pcm: seconds(6) }]);
	const live = await firstOf(page, 2621, 'live');
	await dock(page).locator('.player').getByRole('button', { name: /^Add to basket: Dispatch North/ }).click();
	await expect(dock(page).locator('.player').getByRole('button', { name: /^Add to basket: Dispatch North/ })).toHaveAttribute('aria-pressed', 'true');
	expect(JSON.parse((await page.evaluate((k) => localStorage.getItem(k), KEY)) ?? '{}').calls.map((c: { id: string }) => c.id)).toEqual([live.call_id]);
	// The basket, reached from the menu so the player keeps playing: the
	// call is there while live, and downloads once its recording is in.
	await page.getByRole('navigation').getByRole('link', { name: /^Basket/ }).first().click();
	const it = item(page, live.call_id);
	await expect(it.locator('.tg')).toHaveText('Dispatch North');
	await sending;
	await expect(it).toHaveAttribute('data-state', 'recorded', { timeout: 30_000 });
	await expect(it.getByRole('link', { name: /^Download: Dispatch North/ })).toBeVisible();
	expect(await downloadWhole(page)).toEqual([live.call_id]);
});

test('@live R64: a conversation plays in order in the dock', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	const a = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1001', '2631', '2m');
	const b = await fixture('call-dated', 'Bench', env('RA_RECORDINGS'), '1002', '2632', '1m');
	await page.addInitScript(() => {
		const order: string[] = [];
		(window as unknown as { order: string[] }).order = order;
		addEventListener('radioarchive:first-frame', (e) => order.push((e as CustomEvent).detail.call_id));
	});
	await withBasket(page, [a, b], [{ id: 'c1', name: 'Structure fire', calls: [a, b] }]);
	await page.goto('/basket');
	const conv = page.getByRole('article', { name: 'Structure fire' });
	await expect(conv.getByRole('listitem')).toHaveCount(2);
	await expect(conv.locator('[data-state="recorded"]')).toHaveCount(2);
	await conv.getByRole('button', { name: 'Play all' }).click();
	await expect(dock(page).locator('.replay')).toContainText('Structure fire: call 1 of 2');
	await expect(dock(page).locator('.replay')).toContainText('Structure fire: call 2 of 2', { timeout: 15_000 });
	await expect(dock(page).locator('.replay')).toHaveCount(0, { timeout: 15_000 });
	expect(await page.evaluate(() => (window as unknown as { order: string[] }).order)).toEqual([a, b]);
});
