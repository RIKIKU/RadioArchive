/// <reference types="node" />
// The queue, its controls and now playing in the dock (issue #60; SPEC.md
// R15, R22, R23, R31, R43, R44, R45, R48, R81; A8, A9, A11, A14, A27; ADR
// 0012), with the whole stack: the fake recorder's calls through the
// recorder client and the binary's live feed to the dock's player. An
// admin's changes, which the binary can't make yet (M4), are made in the
// database by the fixture's command, and the live feed sees them as it sees
// any writer's (ADR 0007).
//
// scripts/ci/e2e-web.sh runs these with the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { dock, firstOf, heard, listen, playedFrom, recorderDeaf, seconds, sendCalls, setTalkgroup, tone } from './live.ts';

const NOTICE = "Some talkgroups aren't being received right now.";
const queued = (page: Page) => dock(page).locator('.queued');
const badge = (page: Page) => dock(page).locator('.live-badge');

test('@live A8: a priority call plays next without cutting off the call playing; the queued call then plays from its recording', async ({ page }) => {
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002, priority: true }]);
	// A call on 1001 plays; another on 1001 starts, then one on 1002 (priority).
	const sending = sendCalls([
		{ unit: 5201, atMs: 200, pcm: seconds(4) },
		{ unit: 5202, atMs: 1000, pcm: [tone] },
		{ unit: 5203, talkgroup: 1002, atMs: 1500, pcm: [tone] }
	]);
	await firstOf(page, 5201, 'live');
	await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });
	// Now playing: the talkgroup, its group, the unit, Live, the duration so far.
	await expect(dock(page).locator('.tg')).toHaveText('Dispatch North');
	await expect(dock(page).locator('.group')).toHaveText('North');
	await expect(dock(page).locator('.unit')).toHaveText('5201');
	await expect(badge(page)).toHaveText('Live');
	await expect(dock(page).locator('.elapsed')).toHaveText(/Duration so far \d:\d\d/);
	await sending;

	const first = await playedFrom(page, 5201, 'live');
	const priority = await playedFrom(page, 5203, 'recording');
	const rest = await playedFrom(page, 5202, 'recording');
	const h = await heard(page);
	// In that order, each once: the 1001 call wasn't cut off, and played to its end.
	expect(h.order).toEqual([first.first.call_id, priority.first.call_id, rest.first.call_id]);
	expect(first.played.frames, 'the first call, all 4 s of it').toBeGreaterThanOrEqual(150);
	expect(h.ended).toEqual([]);
	await expect(queued(page)).toHaveText('No calls queued');
});

test('@live A8: Skip moves to the next call, Jump to live empties the queue, Pause keeps queuing', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page);

	// Skip.
	let sending = sendCalls([
		{ unit: 5211, atMs: 200, pcm: seconds(8) },
		{ unit: 5212, atMs: 1000, pcm: seconds(3) }
	]);
	const skipped = await firstOf(page, 5211, 'live');
	await expect(queued(page)).toHaveText('1 queued', { timeout: 10_000 });
	await dock(page).getByRole('button', { name: 'Skip' }).click();
	await expect.poll(async () => (await heard(page)).ended).toEqual([expect.objectContaining({ call_id: skipped.call_id, how: 'skipped' })]);
	// The next call plays at once, from its start: it's still live.
	await firstOf(page, 5212, 'live', 5_000);
	await expect(queued(page)).toHaveText('No calls queued');
	await sending;
	await playedFrom(page, 5212, 'live');
	expect((await heard(page)).played.some((p) => p.call_id === skipped.call_id), 'the skipped call never finishes').toBe(false);

	// Jump to live.
	sending = sendCalls([
		{ unit: 5221, atMs: 200, pcm: seconds(6) },
		{ unit: 5222, atMs: 800, pcm: [tone] },
		{ unit: 5223, atMs: 1200, pcm: [tone] }
	]);
	await firstOf(page, 5221, 'live');
	await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });
	await dock(page).getByRole('button', { name: 'Jump to live' }).click();
	await expect(queued(page)).toHaveText('No calls queued');
	await expect.poll(async () => (await heard(page)).ended.filter((e) => e.how === 'cleared').length).toBe(2);
	// The call playing is live, so it plays on.
	await sending;
	await playedFrom(page, 5221, 'live');
	await page.waitForTimeout(2_000);
	expect((await heard(page)).first.filter((f) => f.unit === 5222 || f.unit === 5223)).toEqual([]);

	// Pause.
	sending = sendCalls([
		{ unit: 5231, atMs: 200, pcm: seconds(6) },
		{ unit: 5232, atMs: 1500, pcm: [tone] }
	]);
	const paused = await firstOf(page, 5231, 'live');
	await dock(page).getByRole('button', { name: 'Pause' }).click();
	await expect(dock(page).getByRole('button', { name: 'Resume' })).toBeVisible();
	// Calls keep queuing; nothing finishes while paused.
	await expect(queued(page)).toHaveText('1 queued', { timeout: 10_000 });
	await sending;
	await page.waitForTimeout(1_000);
	expect((await heard(page)).played.some((p) => p.call_id === paused.call_id)).toBe(false);
	// Resume carries on where it paused, behind the radio now, then through the queue.
	await dock(page).getByRole('button', { name: 'Resume' }).click();
	await expect(badge(page)).toHaveText(/^Behind by \d+ s$/);
	const whole = await playedFrom(page, 5231, 'live');
	expect(whole.played.frames, 'all 6 s of the paused call').toBeGreaterThanOrEqual(250);
	const next = await playedFrom(page, 5232, 'recording');
	expect((await heard(page)).order.slice(-2)).toEqual([paused.call_id, next.first.call_id]);
});

test("@live A9: with hold on, other talkgroups' calls aren't queued, and the saved set is unchanged", async ({ page }) => {
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	const saved = await page.evaluate(() => localStorage.getItem('radioarchive.listening-set'));
	const sets: unknown[] = [];
	await page.exposeFunction('setSent', (s: unknown) => sets.push(s));
	await page.evaluate(() => addEventListener('radioarchive:listening-set', (e) => (window as unknown as { setSent(s: unknown): void }).setSent((e as CustomEvent).detail)));

	const playing = sendCalls([{ unit: 5241, atMs: 200, pcm: seconds(8) }]);
	await firstOf(page, 5241, 'live');
	const hold = dock(page).getByRole('button', { name: 'Hold' });
	await hold.click();
	await expect(hold).toHaveAttribute('aria-pressed', 'true');
	await expect(dock(page).getByText('Hold on: Dispatch North only')).toBeVisible();

	// Calls on 1002 aren't queued; one on 1001, the held talkgroup, is.
	await sendCalls([
		{ unit: 5242, talkgroup: 1002, atMs: 200, pcm: [tone] },
		{ unit: 5243, talkgroup: 1001, atMs: 400, pcm: [tone] },
		{ unit: 5244, talkgroup: 1002, atMs: 600, pcm: [tone] }
	]);
	await expect(queued(page)).toHaveText('1 queued');
	await playing;
	await playedFrom(page, 5241, 'live');
	await playedFrom(page, 5243, 'recording');
	await page.waitForTimeout(2_000);
	expect((await heard(page)).first.filter((f) => f.unit === 5242 || f.unit === 5244)).toEqual([]);

	// Hold off. The saved set, and what the feed hears, never changed.
	await hold.click();
	await expect(hold).toHaveAttribute('aria-pressed', 'false');
	await expect(dock(page).getByText(/Hold on/)).toHaveCount(0);
	expect(await page.evaluate(() => localStorage.getItem('radioarchive.listening-set'))).toBe(saved);
	expect(sets).toEqual([]);
});

test('@live A11: hiding 1001 mid-call stops the audio and removes its queued call within 1 s', async ({ page }) => {
	await listen(page);
	const sending = sendCalls([
		{ unit: 5251, atMs: 200, pcm: seconds(8) },
		{ unit: 5252, atMs: 1000, pcm: seconds(4) }
	]);
	const live = await firstOf(page, 5251, 'live');
	await expect(queued(page)).toHaveText('1 queued', { timeout: 10_000 });
	try {
		const hidden = await setTalkgroup(1001, 'state', 'hidden');
		await expect.poll(async () => (await heard(page)).ended.filter((e) => e.how === 'withdrawn').length, { timeout: 5_000 }).toBe(2);
		const withdrawn = (await heard(page)).ended;
		console.log(`A11: withdrawn ${withdrawn.map((e) => `${e.at - hidden} ms`).join(', ')} after the change committed`);
		for (const e of withdrawn) expect(e.at - hidden, 'withdrawn within 1 s of hiding').toBeLessThanOrEqual(1_000);
		expect(withdrawn.map((e) => e.call_id)).toContain(live.call_id);
		// Nothing plays and nothing waits; the dock names nothing of it.
		await expect(queued(page)).toHaveText('No calls queued');
		await expect(dock(page).locator('.tg')).toHaveCount(0);
		await sending;
		expect((await heard(page)).first.filter((f) => f.unit === 5252)).toEqual([]);
	} finally {
		await setTalkgroup(1001, 'state', 'public');
		await sending.catch(() => undefined);
	}
});

test('@live A14: a delayed call is queued once its delay has passed, labelled, and plays from its recording', async ({ page }, info) => {
	test.skip(info.project.name !== 'chromium', 'a minute of waiting: once is enough (the browsers play recordings in the playback tests)');
	test.setTimeout(3 * 60_000);
	await listen(page);
	try {
		await setTalkgroup(1001, 'delay', 1);
		await sendCalls([{ unit: 5261, atMs: 200, pcm: seconds(3) }]);
		const ended = Date.now();
		// Nothing of it live.
		await page.waitForTimeout(5_000);
		expect((await heard(page)).first.filter((f) => f.unit === 5261)).toEqual([]);
		// A minute after it ended: queued, "Delayed 1 min", from its recording.
		await expect(badge(page)).toHaveText('Delayed 1 min', { timeout: 90_000 });
		const { first } = await playedFrom(page, 5261, 'recording', 30_000);
		expect(first.at - ended, 'not before its delay').toBeGreaterThanOrEqual(59_000);
	} finally {
		await setTalkgroup(1001, 'delay', 0);
	}
});

test("@live A27: the recorder notice shows while a recorder is deaf, and clears when it's heard again", async ({ page }) => {
	await listen(page);
	await expect(page.getByText(NOTICE)).toHaveCount(0);
	const heardAgain = recorderDeaf();
	try {
		await expect(page.getByText(NOTICE)).toBeVisible({ timeout: 20_000 });
		// Above the dock, and naming nothing: no recorder, system or talkgroup.
		const notice = page.locator('.notice-place');
		await expect(notice).toHaveText(NOTICE);
		const [n, d] = [await notice.boundingBox(), await dock(page).boundingBox()];
		expect(n!.y + n!.height).toBeLessThanOrEqual(d!.y);
	} finally {
		heardAgain();
	}
	await expect(page.getByText(NOTICE)).toHaveCount(0, { timeout: 20_000 });
});

test('@live A27: a quiet recorder shows nothing', async ({ page }, info) => {
	test.skip(info.project.name !== 'chromium', "a minute of waiting, and the server's answer is the same for every browser");
	test.setTimeout(3 * 60_000);
	// Every notice the feed sends.
	await page.addInitScript(() => {
		const w = window as unknown as { notices: unknown[] };
		w.notices = [];
		const Native = window.WebSocket;
		window.WebSocket = class extends Native {
			constructor(url: string | URL, protocols?: string | string[]) {
				super(url, protocols);
				this.addEventListener('message', (e) => {
					if (typeof e.data === 'string' && e.data.includes('"notice"')) w.notices.push(JSON.parse(e.data));
				});
			}
		};
	});
	await listen(page);
	// Bench's quiet threshold is a minute (testing/webfixture): no calls for
	// over a minute and it's quiet, while its heartbeats stay healthy.
	await page.waitForTimeout(70_000);
	expect(await page.evaluate(() => (window as unknown as { notices: unknown[] }).notices)).toEqual([]);
	await expect(page.getByText(NOTICE)).toHaveCount(0);
});

test("@live R22, R48: with alerts on, a talkgroup's alert sounds when a call starts on it; off by default", async ({ page }) => {
	try {
		await setTalkgroup(1001, 'alert', 'chime');
		await listen(page);
		// Off by default: nothing sounds.
		await sendCalls([{ unit: 5271, atMs: 200, pcm: [tone] }]);
		await playedFrom(page, 5271);
		expect((await heard(page)).alerts).toEqual([]);
		// Switched on in the account menu, beside the look: saved in the browser.
		await page.getByRole('button', { name: 'Account menu, signed out' }).click();
		await page.getByRole('checkbox', { name: /Alert sounds/ }).check();
		expect(await page.evaluate(() => localStorage.getItem('radioarchive.playback'))).toBe(JSON.stringify({ v: 1, alerts: true }));
		await page.keyboard.press('Escape');
		const sent = await sendCalls([{ unit: 5272, atMs: 200, pcm: [tone] }]);
		const { first } = await playedFrom(page, 5272);
		expect((await heard(page)).alerts).toEqual([{ call_id: first.call_id, alert: 'chime' }]);
		expect(sent.size).toBe(1);
	} finally {
		await setTalkgroup(1001, 'alert', 'null');
	}
});

test('@live the dock playing, with a queue, with hold on and with the recorder notice: axe in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const looks = ['harbour', 'console'] as const;
	const themes = ['light', 'dark'] as const;
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	let heardAgain: (() => void) | undefined;
	await setTalkgroup(1001, 'led', 'blue');
	try {
		await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002, priority: true }]);
		const sending = sendCalls([
			{ unit: 5281, atMs: 200, pcm: seconds(90) },
			{ unit: 5282, atMs: 1000, pcm: seconds(5) },
			{ unit: 5283, talkgroup: 1002, atMs: 1500, pcm: seconds(2) }
		]);
		await firstOf(page, 5281, 'live');
		await expect(queued(page)).toHaveText('2 queued', { timeout: 10_000 });
		await expect(dock(page).locator('.led[data-led="blue"]')).toBeVisible();

		const check = async (state: string) => {
			for (const width of [1280, 375]) {
				await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
				for (const look of looks)
					for (const theme of themes) {
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
							await page.screenshot({ path: `${shots}/dock-${state}-${look}-${theme}-${width}.png` });
						}
					}
			}
		};

		// Playing, with two calls queued.
		await check('playing');
		// Hold on: the 1002 call waits until it's off.
		await dock(page).getByRole('button', { name: 'Hold' }).click();
		await expect(queued(page)).toHaveText('2 queued, 1 after hold');
		await check('hold');
		// The recorder notice, above the dock.
		heardAgain = recorderDeaf();
		await expect(page.getByText(NOTICE)).toBeVisible({ timeout: 20_000 });
		await check('notice');

		heardAgain();
		heardAgain = undefined;
		await dock(page).getByRole('button', { name: 'Stop listening' }).click();
		await sending;
	} finally {
		heardAgain?.();
		await setTalkgroup(1001, 'led', 'null');
	}
});
