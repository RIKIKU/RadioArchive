/// <reference types="node" />
// Previous, Catch up and the player bar (SPEC.md R44, R45; A8, A40, A41,
// A36; ADR 0048, 0051), with the whole stack: the fake recorder's calls
// through the recorder client and the binary's live feed to the dock's
// player.
//
// scripts/ci/e2e-web.sh runs these with the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { display, dock, firstOf, heard, listen, playedFrom, seconds, sendCalls, tone } from './live.ts';

const previous = (page: Page) => dock(page).getByRole('button', { name: 'Previous' });
const catchUp = (page: Page) => dock(page).getByRole('button', { name: 'Catch up to live' });

/** The units of the calls that started playing, with how, from the first time unit started on. */
async function heardFrom(page: Page, unit: number) {
	const first = (await heard(page)).first;
	const i = first.findIndex((f) => f.unit === unit);
	return first.slice(i).map((f) => `${f.unit} ${f.source}`);
}

/** Presses Previous n times, quicker than its presses settle. */
async function back(page: Page, n: number) {
	for (let i = 0; i < n; i++) await previous(page).click({ delay: 0 });
}

test('@live A40: Previous goes back a call a press, counts quick presses, then plays every call from there in order, then the queue, then live', async ({ page }) => {
	test.setTimeout(3 * 60_000);
	await listen(page);
	await expect(previous(page)).toBeDisabled();

	// Three calls, one after another, each heard live.
	await sendCalls([
		{ unit: 5701, atMs: 200, pcm: [tone] },
		{ unit: 5702, atMs: 2500, pcm: [tone] },
		{ unit: 5703, atMs: 5000, pcm: [tone] }
	]);
	for (const u of [5701, 5702, 5703]) await playedFrom(page, u, 'live');
	const first = (await heard(page)).first.find((f) => f.unit === 5701)!;

	// A fourth playing, and a fifth that starts while Previous plays.
	const sending = sendCalls([
		{ unit: 5704, atMs: 200, pcm: seconds(6) },
		{ unit: 5705, atMs: 3000, pcm: [tone] }
	]);
	const playing = await firstOf(page, 5704, 'live');
	await expect(previous(page)).toBeEnabled();
	await back(page, 3);
	await expect(dock(page).getByText('Back 3 calls…')).toBeVisible();
	// Acted on once, after the last press: three calls back.
	await expect.poll(async () => (await heard(page)).previous).toEqual([{ call_id: first.call_id, steps: 3 }]);
	await expect(dock(page).getByText(/Back \d calls?…/)).toHaveCount(0);
	// The first call heard: nothing earlier.
	await expect(previous(page)).toBeDisabled();
	await expect(catchUp(page)).toBeEnabled();
	await sending;

	// Every call from there, in order, then the call that queued meanwhile.
	// No live call cut in: 5705 waited its turn.
	await expect.poll(async () => (await heardFrom(page, 5704)).map((s) => s.split(' ')[0]), { timeout: 60_000 }).toEqual(['5704', '5701', '5702', '5703', '5704', '5705']);
	const h = await heard(page);
	expect(h.ended, 'only the call playing at the presses stopped, once').toEqual([expect.objectContaining({ call_id: playing.call_id, how: 'skipped' })]);
	const sources = await heardFrom(page, 5704);
	expect(sources.slice(1, 4)).toEqual(['5701 recording', '5702 recording', '5703 recording']);
	const again = await playedFrom(page, 5704, sources[4].split(' ')[1]);
	expect(again.played.frames, 'all 6 s of the call played again').toBeGreaterThanOrEqual(270);
	await playedFrom(page, 5705, 'recording');

	// Caught up: live carries on by itself.
	await expect(catchUp(page)).toBeDisabled({ timeout: 10_000 });
	await sendCalls([{ unit: 5706, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5706, 'live');
	expect((await heardFrom(page, 5704)).at(-1)).toBe('5706 live');
});

test('@live A40: a call still on air when Previous reaches it again plays from its start, from the frames heard, and carries on live', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page);
	await sendCalls([{ unit: 5711, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5711, 'live');

	let ended = 0;
	const sending = sendCalls([{ unit: 5712, atMs: 200, pcm: seconds(12) }]).then((s) => (ended = Date.now()) && s);
	await firstOf(page, 5712, 'live');
	await page.waitForTimeout(1_000);
	await back(page, 1);
	await playedFrom(page, 5711, 'recording');
	// Its turn again while it's still on air: the frames heard so far, then live.
	await expect.poll(async () => (await heardFrom(page, 5712)).filter((s) => s.startsWith('5712')).length, { timeout: 15_000 }).toBe(2);
	expect(ended, 'played again before it ended').toBe(0);
	expect((await heardFrom(page, 5712)).at(-1)).toBe('5712 live');
	await sending;
	const { played } = await playedFrom(page, 5712, 'live');
	expect(played.frames, 'all 12 s of it, from its start').toBeGreaterThanOrEqual(540);
	expect((await heard(page)).ended.filter((e) => e.how === 'skipped').length).toBe(1);
});

test('@live A40: with hold on, Previous steps through the held talkgroup\'s calls only', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	await sendCalls([
		{ unit: 5721, atMs: 200, pcm: [tone] },
		{ unit: 5722, talkgroup: 1002, atMs: 2500, pcm: [tone] },
		{ unit: 5723, atMs: 5000, pcm: [tone] }
	]);
	for (const u of [5721, 5722, 5723]) await playedFrom(page, u, 'live');
	const first = (await heard(page)).first.find((f) => f.unit === 5721)!;

	const sending = sendCalls([{ unit: 5724, atMs: 200, pcm: seconds(8) }]);
	await firstOf(page, 5724, 'live');
	await dock(page).getByRole('button', { name: 'Hold' }).click();
	await expect(display(page).locator('.hold')).toHaveText('Hold on');
	// Two steps back on 1001 pass over 1002's call.
	await back(page, 2);
	await expect.poll(async () => (await heard(page)).previous).toEqual([{ call_id: first.call_id, steps: 2 }]);
	await expect(previous(page)).toBeDisabled();
	await sending;
	await expect.poll(async () => (await heardFrom(page, 5724)).map((s) => s.split(' ')[0]), { timeout: 30_000 }).toEqual(['5724', '5721', '5723', '5724']);
	await dock(page).getByRole('button', { name: 'Hold' }).click();
});

test('@live A41: Catch up leaves Previous\'s calls and goes back to live at once', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page);
	await sendCalls([
		{ unit: 5731, atMs: 200, pcm: seconds(3) },
		{ unit: 5732, atMs: 4500, pcm: [tone] }
	]);
	await playedFrom(page, 5731, 'live');
	await playedFrom(page, 5732, 'live');
	// Nothing playing: two presses go to the call before the last.
	await back(page, 2);
	const replayed = await firstOf(page, 5731, 'recording');
	await expect(catchUp(page)).toBeEnabled();
	await catchUp(page).click();
	await expect.poll(async () => (await heard(page)).ended).toEqual([expect.objectContaining({ call_id: replayed.call_id, how: 'cleared' })]);
	await expect(catchUp(page)).toBeDisabled();
	await sendCalls([{ unit: 5733, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5733, 'live');
	// 5732 wasn't played again.
	expect((await heard(page)).first.filter((f) => f.unit === 5732).length).toBe(1);
});

test('@live the player bar: its order, in one row at 1,280 px beside the side menu; short names and the Actions menu on a phone; axe in both looks and themes', async ({ page }, info) => {
	test.setTimeout(3 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await listen(page);
	await sendCalls([{ unit: 5741, atMs: 200, pcm: [tone] }]);
	await playedFrom(page, 5741, 'live');
	const sending = sendCalls([
		{ unit: 5742, atMs: 200, pcm: seconds(40) },
		{ unit: 5743, atMs: 1000, pcm: [tone] }
	]);
	// Whatever happens, the recorder has finished this test's calls before the next test.
	try {
		await barChecks(page, shots);
	} finally {
		await sending.catch(() => undefined);
	}
});

async function barChecks(page: Page, shots: string) {
	await firstOf(page, 5742, 'live');
	await expect(display(page).locator('.queued')).toHaveText('1 queued', { timeout: 10_000 });
	const controls = dock(page).getByRole('group', { name: 'Player controls' });
	const rows = async () => new Set(await controls.getByRole('button').evaluateAll((els) => els.map((e) => Math.round(e.getBoundingClientRect().top)))).size;
	const names = () => controls.getByRole('button').evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') ?? e.textContent?.trim()));

	const check = async (state: string, width: number) => {
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
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
				expect(results.violations, `${state}, ${look} ${theme}, ${width} px`).toEqual([]);
				expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
				if (shots) {
					await page.mouse.move(0, 0);
					await page.screenshot({ path: `${shots}/bar-${state}-${look}-${theme}-${width}.png` });
				}
			}
	};

	// A desktop, with the side menu: every control in one row, names beside icons.
	await page.setViewportSize({ width: 1280, height: 900 });
	await expect(controls.getByRole('button')).toHaveText(['Pause', 'Previous', 'Skip', 'Catch up to live', 'Hold', 'Avoid Dispatch North', 'Stop listening', 'Share', 'Add to basket']);
	expect(await controls.getByRole('button').evaluateAll((els) => els.map((e) => e.getAttribute('aria-label') ?? e.textContent?.trim()))).toEqual([
		'Pause',
		'Previous',
		'Skip',
		'Catch up to live',
		'Hold',
		'Avoid Dispatch North',
		'Stop listening',
		'Share: Dispatch North',
		'Add to basket: Dispatch North'
	]);
	expect(await rows(), 'one row at 1,280 px').toBe(1);
	// What Add to basket did shows without moving the row.
	await controls.getByRole('button', { name: /^Add to basket/ }).click();
	await expect(dock(page).getByText('Added to the basket')).toBeVisible();
	expect(await rows(), 'still one row').toBe(1);
	await controls.getByRole('button', { name: /^Add to basket/ }).click();
	await check('desktop', 1280);

	// A narrower desktop: short names under the icons, still one row, with
	// Share and Add to basket in Actions, as on a phone (ADR 0055).
	await page.setViewportSize({ width: 1024, height: 768 });
	expect(await rows(), 'one row at 1,024 px').toBe(1);
	expect(await names()).toEqual(['Pause', 'Previous', 'Skip', 'Catch up to live', 'Hold', 'Avoid Dispatch North', 'Stop listening', 'More actions: Dispatch North']);
	// Wide ones, in one row with Resume's longer name: short names at 1,600
	// px, and names beside the icons at 1,700 px.
	await page.setViewportSize({ width: 1600, height: 900 });
	await controls.getByRole('button', { name: 'Pause' }).click();
	await expect(controls.getByRole('button', { name: 'Resume' })).toBeVisible();
	expect(await rows(), 'one row at 1,600 px').toBe(1);
	await page.setViewportSize({ width: 1700, height: 900 });
	expect(await rows(), 'one row at 1,700 px').toBe(1);
	expect(await controls.getByRole('button', { name: 'Resume' }).evaluate((b) => getComputedStyle(b).flexDirection), 'names beside the icons').toBe('row');
	await controls.getByRole('button', { name: 'Resume' }).click();

	// A phone: short names under the icons; Share, Add to basket and Stop
	// listening in Actions (ADR 0055).
	await page.setViewportSize({ width: 375, height: 812 });
	expect(await names()).toEqual(['Pause', 'Previous', 'Skip', 'Catch up to live', 'Hold', 'Avoid Dispatch North', 'More actions: Dispatch North']);
	await expect(controls.getByText('Actions', { exact: true })).toBeVisible();
	expect(await rows(), 'one row on a phone').toBe(1);
	const target = await page.evaluate(() => {
		const d = document.createElement('div');
		d.style.width = 'var(--target)';
		document.body.append(d);
		const w = d.getBoundingClientRect().width;
		d.remove();
		return w;
	});
	for (const b of await controls.getByRole('button').evaluateAll((els) => els.map((e) => e.getBoundingClientRect()).map((r) => Math.min(r.width, r.height))))
		expect(b, 'whole targets').toBeGreaterThanOrEqual(target - 0.5);
	await check('phone', 375);
	await controls.getByRole('button', { name: 'More actions: Dispatch North' }).click();
	await expect(dock(page).getByRole('button', { name: 'Share link' })).toBeVisible();
	await check('phone-actions', 375);
	await expect(dock(page).getByRole('button', { name: 'Stop listening' })).toBeVisible();
	await dock(page).getByRole('button', { name: 'Add to basket' }).click();
	await expect(dock(page).locator('.said-place')).toContainText('Added to the basket');

	await controls.getByRole('button', { name: 'More actions: Dispatch North' }).click();
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
	await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
}
