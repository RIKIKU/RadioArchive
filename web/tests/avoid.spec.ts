/// <reference types="node" />
// Turning talkgroups off dequeues their calls, and Avoid (SPEC.md R43, R44;
// A44; ADR 0054), with the whole stack: the fake recorder's calls through
// the recorder client and the binary's live feed to the player.
//
// scripts/ci/e2e-web.sh runs these with the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { display, dock, firstOf, heard, listen, playedFrom, seconds, sendCalls, tone } from './live.ts';

const SAVED = 'radioarchive.listening-set';
const queued = (page: Page) => display(page).locator('.queued');
const saved = (page: Page) => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? 'null')?.talkgroups ?? [], SAVED);
const avoidButton = (page: Page) => dock(page).getByRole('button', { name: /^Avoid/ });
const toast = (page: Page) => page.locator('.notice-place .avoid-toast');
/** The calls that left the queue, or stopped, because their talkgroups were turned off. */
const off = async (page: Page) => (await heard(page)).ended.filter((e) => (e.how as string) === 'off').map((e) => e.call_id);
/** The units of every call that started playing. */
const units = async (page: Page) => (await heard(page)).first.map((f) => f.unit);

/** Waits for the recorder to finish this test's calls whatever happens, so the next test starts clean. */
async function finally_(sending: Promise<unknown>, body: () => Promise<void>) {
	try {
		await body();
	} finally {
		await sending.catch(() => undefined);
	}
}

test('@live A44: a queue built while paused empties when All off is pressed, the call playing stops, and All on brings none of it back', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	const sending = sendCalls([
		{ unit: 5801, atMs: 200, pcm: seconds(14) },
		{ unit: 5802, atMs: 1500, pcm: seconds(12) },
		{ unit: 5803, talkgroup: 1002, atMs: 2000, pcm: seconds(12) }
	]);
	await finally_(sending, async () => {
		const playing = await firstOf(page, 5801, 'live');
		await dock(page).getByRole('button', { name: 'Pause' }).click();
		await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });

		await page.getByRole('button', { name: 'All off' }).click();
		await expect(queued(page)).toHaveText('0 queued');
		// The two queued and the one playing, each once.
		await expect.poll(async () => (await off(page)).length).toBe(3);
		expect(await off(page)).toContain(playing.call_id);

		// On again while those calls are still on air: none of them comes back.
		await page.getByRole('button', { name: 'All on' }).click();
		await dock(page).getByRole('button', { name: 'Resume' }).click();
		await page.waitForTimeout(3_000);
		await expect(queued(page)).toHaveText('0 queued');
		expect(await units(page)).toEqual([5801]);
		expect((await heard(page)).played.map((p) => p.call_id)).toEqual([]);
	});
	// A new call plays as ever.
	await sendCalls([{ unit: 5804, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5804, 'live');
});

test("@live A44: turning one agency's tile off takes only its calls out of the queue, and stops its call playing", async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	const sending = sendCalls([
		{ unit: 5811, atMs: 200, pcm: seconds(10) },
		{ unit: 5812, talkgroup: 1002, atMs: 1000, pcm: seconds(6) },
		{ unit: 5813, atMs: 1500, pcm: [tone] }
	]);
	await finally_(sending, async () => {
		const playing = await firstOf(page, 5811, 'live');
		await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });
		// North is 1001's agency; 1002 is in no group.
		const north = page.getByRole('list', { name: 'Agencies' }).getByRole('button', { name: /^North/ });
		await north.click();
		await expect(north).toHaveAttribute('aria-pressed', 'false');
		expect(await saved(page)).toEqual([{ id: 1002 }]);
		// 1001's call playing stops and its queued call leaves; 1002's plays next, still live.
		await expect.poll(async () => (await off(page)).length).toBe(2);
		expect(await off(page)).toContain(playing.call_id);
		await firstOf(page, 5812, 'live', 5_000);
		await expect(display(page).locator('.name')).toHaveText('Fireground 41');
		await expect(queued(page)).toHaveText('0 queued');
	});
	await playedFrom(page, 5812, 'live');
	expect(await units(page)).toEqual([5811, 5812]);
});

test('@live A44: turning the held talkgroup off from the search lets hold go', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	const sending = sendCalls([{ unit: 5821, atMs: 200, pcm: seconds(10) }]);
	await finally_(sending, async () => {
		await firstOf(page, 5821, 'live');
		const hold = dock(page).getByRole('button', { name: 'Hold' });
		await hold.click();
		await expect(hold).toHaveAttribute('aria-pressed', 'true');

		await page.getByLabel('Search talkgroups').fill('1001');
		const chip = page.getByRole('list', { name: 'Search results' }).getByRole('button', { name: /^Dispatch North / });
		await chip.click();
		await expect(chip).toHaveAttribute('aria-pressed', 'false');
		await expect(hold).toHaveAttribute('aria-pressed', 'false');
		await expect(display(page).locator('.hold')).toHaveText('Hold off');
		await expect(hold).toBeDisabled();
		await expect.poll(() => off(page)).toHaveLength(1);
	});
	// Hold gone, 1002's calls are heard again.
	await sendCalls([{ unit: 5822, talkgroup: 1002, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5822, 'live');
});

test('@live A44: Avoid turns the talkgroup playing off, skips its call and takes its calls out of the queue; Undo turns it back on, priority mark and all', async ({ page }) => {
	test.setTimeout(3 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001, priority: true }, { id: 1002 }]);
	// Nothing playing: nothing to avoid.
	await expect(avoidButton(page)).toBeDisabled();
	await expect(avoidButton(page)).toHaveAccessibleName('Avoid');

	const sending = sendCalls([
		{ unit: 5831, atMs: 200, pcm: seconds(10) },
		{ unit: 5832, atMs: 1000, pcm: seconds(8) },
		{ unit: 5833, talkgroup: 1002, atMs: 1500, pcm: seconds(4) }
	]);
	await finally_(sending, async () => {
		const playing = await firstOf(page, 5831, 'live');
		await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });
		await expect(avoidButton(page)).toBeEnabled();
		await expect(avoidButton(page)).toHaveAccessibleName('Avoid Dispatch North');
		const avoided = page.evaluate(() => new Promise((r) => addEventListener('radioarchive:avoided', (e) => r((e as CustomEvent).detail), { once: true })));
		await avoidButton(page).click();
		expect(await avoided).toEqual({ call_id: playing.call_id, talkgroup: { id: 1001 }, from: 'set' });

		// Off in the saved set, the call playing stopped, its queued call gone; 1002's plays.
		expect(await saved(page)).toEqual([{ id: 1002 }]);
		await expect.poll(async () => (await off(page)).length).toBe(2);
		expect(await off(page)).toContain(playing.call_id);
		await firstOf(page, 5833, 'live', 5_000);
		await expect(queued(page)).toHaveText('0 queued');

		// The toast, polite, with Undo; axe in both themes.
		await expect(toast(page).locator('p')).toHaveText('Avoided Dispatch North·');
		await expect(page.locator('.notice-place')).toHaveAttribute('role', 'status');
		// The pointer on it holds it while it's checked.
		await toast(page).hover();
		const shots = process.env.RA_SHOTS && test.info().project.name === 'chromium' ? process.env.RA_SHOTS : '';
		if (shots) {
			for (const width of [1280, 375]) {
				await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
				await toast(page).hover();
				for (const theme of ['light', 'dark']) {
					await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
					await page.screenshot({ path: `${shots}/avoid-toast-${theme}-${width}.png` });
				}
			}
			await page.setViewportSize({ width: 1280, height: 720 });
			await toast(page).hover();
		}
		for (const theme of ['light', 'dark']) {
			await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
			const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
			expect(results.violations, theme).toEqual([]);
		}
		const undo = toast(page).getByRole('button', { name: 'Undo: turn Dispatch North back on' });
		await undo.click();
		await expect(toast(page)).toHaveCount(0);
		// Back on, priority mark and all; the calls that left stay gone.
		await expect.poll(() => saved(page)).toEqual([{ id: 1002 }, { id: 1001, priority: true }]);
		await page.waitForTimeout(2_000);
		await expect(queued(page)).toHaveText('0 queued');
		expect(await units(page)).toEqual([5831, 5833]);
	});
	await playedFrom(page, 5833, 'live');

	// The toast goes by itself after about 6 s, and a new 1001 call is heard again.
	const sending2 = sendCalls([{ unit: 5834, atMs: 200, pcm: seconds(4) }]);
	await finally_(sending2, async () => {
		await firstOf(page, 5834, 'live');
		await avoidButton(page).click();
		await expect(toast(page)).toBeVisible();
		await page.mouse.move(0, 0);
		const shown = Date.now();
		await expect(toast(page)).toHaveCount(0, { timeout: 10_000 });
		expect(Date.now() - shown, 'about 6 s').toBeGreaterThan(4_500);
		expect(await saved(page)).toEqual([{ id: 1002 }]);
	});
});

test('@live A44: Previous still plays a call of a talkgroup turned off since; the calls it plays forward through stay too', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	await sendCalls([
		{ unit: 5841, atMs: 200, pcm: [tone] },
		{ unit: 5842, talkgroup: 1002, atMs: 2500, pcm: [tone] }
	]);
	await playedFrom(page, 5841, 'live');
	await playedFrom(page, 5842, 'live');
	// Back to 1001's call, then 1001 off: it plays on, as the listener asked for it.
	const previous = dock(page).getByRole('button', { name: 'Previous' });
	await previous.click();
	await previous.click();
	await expect.poll(async () => (await heard(page)).previous.length).toBe(1);
	await page.getByRole('list', { name: 'Agencies' }).getByRole('button', { name: /^North/ }).click();
	await playedFrom(page, 5841, 'recording');
	await playedFrom(page, 5842, 'recording');
	expect(await off(page)).toEqual([]);
});
