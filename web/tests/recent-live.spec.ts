/// <reference types="node" />
// The Recent calls page with the whole stack (issue #79; SPEC.md R23, R46;
// A11, A14; ADR 0012, ADR 0016): replaying a call while live listening
// pauses and keeps queuing, with the fake recorder's calls through the
// recorder client; and hiding or delaying a talkgroup as an admin would,
// with the fixture's command. scripts/ci/e2e-web.sh runs these with the
// playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import { audible, dock, env, heard, listen, playedFrom, seconds, sendCalls, setTalkgroup } from './live.ts';
import { execFile } from 'node:child_process';

const rows = (page: Page) => page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem');
const queued = (page: Page) => dock(page).locator('.queued');

/** Stores one call on a talkgroup from unit, ending now; resolves with its ID and when it ended (Unix ms). */
function storeCall(talkgroup: number, unit: number): Promise<{ id: string; ended: number }> {
	return new Promise((resolve, reject) =>
		execFile(env('RA_WEBFIXTURE'), [env('RA_DATABASE_URL'), 'call', 'Bench', env('RA_RECORDINGS'), String(talkgroup), String(unit)], { timeout: 30_000 }, (err, out, stderr) => {
			if (err) return reject(new Error(`${err.message}\n${stderr}`));
			const [id, ended] = out.trim().split(' ');
			resolve({ id, ended: Number(ended) });
		})
	);
}

/** The Recent calls page, reached from the menu, so the player keeps playing. */
async function toRecent(page: Page) {
	await page.getByRole('navigation').getByRole('link', { name: 'Recent calls' }).first().click();
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
}

test('@live R46: pressing a call replays it from its recording; live listening pauses and calls keep queuing', async ({ page }) => {
	test.setTimeout(3 * 60_000);
	await listen(page);
	await sendCalls([{ unit: 5301, atMs: 200, pcm: seconds(8) }]);
	await playedFrom(page, 5301, 'live', 30_000);
	await toRecent(page);
	// The call just heard is the newest of the listening set.
	const first = (await heard(page)).first.find((f) => f.unit === 5301)!;
	const row = page.locator(`[data-call-id="${first.call_id}"]`);
	await expect(row).toBeVisible({ timeout: 15_000 });
	await expect(row.locator('.units')).toHaveText('Unit 5301');
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();

	// Replaying: from its recording, in the dock; live listening paused.
	await expect(dock(page).locator('.replay')).toContainText('Dispatch North');
	await expect(dock(page).getByText('Paused. Calls keep queuing.')).toBeVisible();
	await expect(row).toHaveAttribute('data-playing', 'yes');
	await expect
		.poll(async () => (await heard(page)).first.filter((f) => f.call_id === first.call_id && (f as { replay?: boolean }).replay).length, { timeout: 15_000 })
		.toBe(1);
	// A live call meanwhile is queued, not heard.
	const sending = sendCalls([{ unit: 5302, atMs: 200, pcm: seconds(2) }]);
	await expect(queued(page)).toHaveText('1 queued', { timeout: 15_000 });
	expect((await heard(page)).first.filter((f) => f.unit === 5302)).toEqual([]);
	await sending;

	// The replay plays to its end, audibly, and live listening waits for the listener.
	let played: { frames: number; peak: number; path: string; replay?: boolean } | undefined;
	await expect
		.poll(
			async () => {
				played = (await heard(page)).played.find((p) => p.call_id === first.call_id && (p as { replay?: boolean }).replay);
				return !!played;
			},
			{ timeout: 30_000 }
		)
		.toBe(true);
	audible(played as never, 'webcodecs');
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect(dock(page).getByRole('button', { name: 'Resume' })).toBeVisible();
	await expect(queued(page)).toHaveText('1 queued');

	// Resume: the call that queued meanwhile plays, from its recording.
	await dock(page).getByRole('button', { name: 'Resume' }).click();
	await playedFrom(page, 5302, 'recording', 30_000);

	// Stop replay stops it at once; Resume while replaying stops it too.
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toBeVisible();
	await dock(page).getByRole('button', { name: 'Stop replay' }).click();
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect.poll(async () => (await heard(page)).ended.filter((e) => e.call_id === first.call_id && (e as { how: string }).how === 'stopped').length).toBe(1);
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toBeVisible();
	await dock(page).getByRole('button', { name: 'Resume' }).click();
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect(dock(page).getByRole('button', { name: 'Pause' })).toBeVisible();
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
});

test('@live R46: a call replays with live listening never started, and starting it stops the replay', async ({ page }) => {
	await page.addInitScript(() => localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] })));
	await page.goto('/recent');
	await expect(rows(page).first()).toBeVisible();
	await rows(page).first().getByRole('button', { name: /^Play: / }).click();
	await expect(dock(page).locator('.replay')).toBeVisible();
	await expect(dock(page).getByRole('button', { name: 'Start listening' })).toBeVisible();
	await dock(page).getByRole('button', { name: 'Start listening' }).click();
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect(dock(page).getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
});

test('@live A11, recent calls half in the browser: once 1001 is hidden its calls are gone from the list; made public, they are back', async ({ page }) => {
	await page.goto('/recent?talkgroups=1001');
	await expect(rows(page).first()).toBeVisible();
	const before = await rows(page).count();
	try {
		await setTalkgroup(1001, 'state', 'hidden');
		await page.reload();
		await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
		await page.goto('/recent?talkgroups=all');
		await expect(page.getByText(/That's every call|Load more/)).toBeVisible();
		const text = await page.locator('body').innerText();
		for (const s of ['Dispatch North', 'DISP N', '1001']) expect(text, s).not.toContain(s);
	} finally {
		await setTalkgroup(1001, 'state', 'public');
	}
	await page.goto('/recent?talkgroups=1001');
	await expect(rows(page)).toHaveCount(before);
});

test("@live A14, recent calls half in the browser: a delayed call isn't listed until its delay has passed", async ({ page }, info) => {
	test.skip(info.project.name !== 'chromium', 'a minute of waiting: once is enough');
	test.setTimeout(3 * 60_000);
	try {
		await setTalkgroup(1001, 'delay', 1);
		const { id, ended } = await storeCall(1001, 5401);
		await page.goto('/recent?unit=5401');
		await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
		await expect(page.locator(`[data-call-id="${id}"]`)).toHaveCount(0);
		// Still held back just before the minute is up.
		await page.waitForTimeout(Math.max(0, ended + 50_000 - Date.now()));
		await page.reload();
		await expect(page.getByText('No calls match. Try other filters or a wider time range.')).toBeVisible();
		// Listed once it has passed.
		await page.waitForTimeout(Math.max(0, ended + 61_000 - Date.now()));
		await page.reload();
		await expect(page.locator(`[data-call-id="${id}"]`)).toBeVisible();
		expect(Date.now() - ended).toBeGreaterThanOrEqual(60_000);
	} finally {
		await setTalkgroup(1001, 'delay', 0);
	}
});
