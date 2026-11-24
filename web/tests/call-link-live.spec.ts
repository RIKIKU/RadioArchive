/// <reference types="node" />
// A16 and Share from now playing (issue #80; SPEC.md R45, R47, R61, R88;
// ADR 0017), with the fixture's command (RA_WEBFIXTURE) starting, ending
// and deleting a call as the recorder and retention would, and the fake
// recorder's calls through the recorder client. scripts/ci/e2e-web.sh runs
// these with the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { dock, env, firstOf, listen, seconds, sendCalls } from './live.ts';

function fixture(...args: string[]): Promise<string> {
	return new Promise((resolve, reject) =>
		execFile(env('RA_WEBFIXTURE'), [env('RA_DATABASE_URL'), ...args], { timeout: 30_000 }, (err, out, stderr) =>
			err ? reject(new Error(`${err.message}\n${stderr}`)) : resolve(out.trim())
		)
	);
}

const card = (page: Page) => page.locator('.card');

/** Copying records the text instead, in window.copied. */
async function catchCopies(page: Page) {
	await page.addInitScript(() => {
		const w = window as unknown as { copied: string[] };
		w.copied = [];
		Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async (t: string) => void w.copied.push(t) } });
	});
}
const copied = (page: Page) => page.evaluate(() => (window as unknown as { copied: string[] }).copied);

test('@live A16: a call in progress, then ended, then deleted by retention, through its one link', async ({ page, request }) => {
	test.setTimeout(3 * 60_000);
	// The listener follows 1002 only.
	await page.addInitScript(() => localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1002 }] })));
	await catchCopies(page);
	const id = await fixture('call-start', 'Bench', '1001', '2301');
	const hidden = (await fixture('call', 'Bench', env('RA_RECORDINGS'), '1003', '9301')).split(' ')[0];

	// In progress: the card says so and offers live listening, which adds
	// 1001 for the session and starts listening.
	await page.goto(`/calls/${id}`);
	await expect(page.getByRole('heading', { level: 1, name: 'Dispatch North' })).toBeVisible();
	await expect(card(page).locator('.progress')).toHaveText('In progress');
	await expect(dock(page).getByRole('button', { name: 'Start listening' })).toBeVisible();
	await card(page).getByRole('button', { name: 'Listen live' }).click();
	await expect(card(page)).toContainText('Dispatch North is added to your listening set for this session.');
	expect(JSON.parse((await page.evaluate(() => sessionStorage.getItem('radioarchive.session-additions'))) ?? '{}').talkgroups).toEqual([{ id: 1001 }]);
	expect(JSON.parse((await page.evaluate(() => localStorage.getItem('radioarchive.listening-set'))) ?? '{}').talkgroups).toEqual([{ id: 1002 }]);
	await expect(dock(page).getByRole('button', { name: 'Stop listening' })).toBeVisible({ timeout: 15_000 });

	// It ends and its recording arrives: the open card plays it, and so
	// does the same link opened again.
	await fixture('call-end', id, env('RA_RECORDINGS'));
	await expect(card(page).getByRole('button', { name: /^Play: Dispatch North/ })).toBeVisible({ timeout: 15_000 });
	await expect(card(page).locator('.progress')).toHaveCount(0);
	await page.goto(`/calls/${id}`);
	await card(page).getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toContainText('Dispatch North');
	await expect(dock(page).locator('.replay')).toHaveAttribute('data-replay', id);
	// Share from now playing (the replay's): the call's link.
	await dock(page).locator('.replay').getByRole('button', { name: /^Share: Dispatch North/ }).click();
	expect((await copied(page)).at(-1)).toBe(`${new URL(page.url()).origin}/calls/${id}`);
	await dock(page).getByRole('button', { name: 'Stop replay' }).click();

	// Retention deletes it: "This call isn't available", with the same
	// status and body as a hidden call's, over HTTP and in the page.
	await fixture('call-delete', id, env('RA_RECORDINGS'));
	const gone = await request.get(`/calls/${id}`);
	const hid = await request.get(`/calls/${hidden}`);
	expect(gone.status()).toBe(404);
	expect(hid.status()).toBe(404);
	expect(await gone.text()).toBe(await hid.text());
	expect(gone.headers()['content-type']).toBe(hid.headers()['content-type']);
	expect(gone.headers()['cache-control']).toBe(hid.headers()['cache-control']);
	await page.goto(`/calls/${id}`);
	await expect(page.getByRole('heading', { level: 1, name: "This call isn't available" })).toBeVisible();
	const deleted = await page.locator('main').innerHTML();
	await page.goto(`/calls/${hidden}`);
	await expect(page.getByRole('heading', { level: 1, name: "This call isn't available" })).toBeVisible();
	expect(await page.locator('main').innerHTML()).toBe(deleted);
	for (const s of ['1003', 'Admin', 'ADMIN']) expect(await page.locator('body').innerText(), s).not.toContain(s);
});

test('@live Share in now playing copies the live call\'s link, which opens its card', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await catchCopies(page);
	await listen(page);
	const sending = sendCalls([{ unit: 5501, atMs: 200, pcm: seconds(6) }]);
	const first = await firstOf(page, 5501, 'live');
	await dock(page).locator('.player').getByRole('button', { name: /^Share: Dispatch North/ }).click();
	const link = `${new URL(page.url()).origin}/calls/${first.call_id}`;
	expect(await copied(page)).toEqual([link]);
	await sending;
	await page.goto(link);
	await expect(card(page)).toHaveAttribute('data-call-id', first.call_id);
	await expect(page.getByRole('heading', { level: 1, name: 'Dispatch North' })).toBeVisible();
});
