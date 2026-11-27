/// <reference types="node" />
// The Recent calls page with the whole stack (issue #79; SPEC.md R23, R46;
// A11, A14; ADR 0012, ADR 0016, ADR 0023): replaying a call, and play from
// here, while live listening pauses and keeps queuing, then resumes by
// itself, with the fake recorder's calls through the
// recorder client; and hiding or delaying a talkgroup as an admin would,
// with the fixture's command. scripts/ci/e2e-web.sh runs these with the
// playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import { audible, display, dock, env, heard, listen, playedFrom, seconds, sendCalls, setTalkgroup, stopListening, waiting } from './live.ts';
import { execFile } from 'node:child_process';
import AxeBuilder from '@axe-core/playwright';

const rows = (page: Page) => page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem');
const queued = (page: Page) => display(page).locator('.queued');

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

/**
 * A phone's dock during a replay (#108): the replay above live listening,
 * each set of controls in one row of whole targets, the replay's details in
 * at most two lines, and the dock no more than a third of the screen.
 */
async function compactDock(page: Page, what: string) {
	// The look's --target, in pixels (ArchiveUx: 44 px on touch screens).
	const target = await page.evaluate(() => {
		const probe = document.body.appendChild(document.createElement('div'));
		probe.style.width = 'var(--target)';
		const w = probe.getBoundingClientRect().width;
		probe.remove();
		return w;
	});
	expect(target).toBeGreaterThan(0);
	const sameRow = async (controls: ReturnType<typeof dock>, n: string) => {
		const boxes = await controls.getByRole('button').evaluateAll((els) => els.map((e) => e.getBoundingClientRect()).map((r) => ({ top: r.top, width: r.width, height: r.height })));
		expect(boxes.length, n).toBeGreaterThan(0);
		for (const b of boxes) {
			expect(b.top, `${n} in one row, ${what}`).toBe(boxes[0].top);
			expect(Math.min(b.width, b.height), `${n} whole targets, ${what}`).toBeGreaterThanOrEqual(target - 0.5);
		}
	};
	const replay = dock(page).locator('.replay');
	await sameRow(dock(page).getByRole('group', { name: 'Replay controls' }), 'the replay controls');
	await sameRow(dock(page).getByRole('group', { name: 'Player controls' }), 'the player controls');
	const [details, line] = await Promise.all([replay.locator('.np-unit').boundingBox(), replay.locator('.np-unit .elapsed').boundingBox()]);
	expect(details!.height, `the replay's details in at most two lines, ${what}`).toBeLessThan(2.5 * line!.height);
	const player = await dock(page).locator('.player').boundingBox();
	expect(player!.y, `live listening under the replay, ${what}`).toBeGreaterThanOrEqual((await replay.boundingBox())!.y + (await replay.boundingBox())!.height);
	const [docked, screen] = [(await dock(page).boundingBox())!, page.viewportSize()!];
	expect(docked.height, `the dock is at most a third of the screen, ${what}`).toBeLessThanOrEqual(screen.height / 3);
}

/** The Recent calls page, reached from the menu, so the player keeps playing. */
async function toRecent(page: Page) {
	await page.getByRole('navigation').getByRole('link', { name: 'Recent calls' }).first().click();
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
}

test('@live R46: pressing a call replays it from its recording; live listening pauses, calls keep queuing, and it resumes by itself after', async ({ page }) => {
	test.setTimeout(4 * 60_000);
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
	await expect(display(page).locator('.np-status')).toHaveText('Paused');
	await expect(row).toHaveAttribute('data-playing', 'yes');
	await expect
		.poll(async () => (await heard(page)).first.filter((f) => f.call_id === first.call_id && (f as { replay?: boolean }).replay).length, { timeout: 15_000 })
		.toBe(1);
	// A live call meanwhile is queued, not heard.
	const sending = sendCalls([{ unit: 5302, atMs: 200, pcm: seconds(2) }]);
	await expect(queued(page)).toHaveText('1 queued', { timeout: 15_000 });
	expect((await heard(page)).first.filter((f) => f.unit === 5302)).toEqual([]);
	await sending;

	// The replay plays to its end, audibly, and live listening resumes by
	// itself (ADR 0016 as amended, ADR 0023).
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
	await expect(dock(page).getByRole('button', { name: 'Pause' })).toBeVisible();
	// The call that queued meanwhile plays, from its recording, with no press.
	await playedFrom(page, 5302, 'recording', 30_000);
	await expect(queued(page)).toHaveText('0 queued');

	// Stop replay stops it at once, and live listening resumes.
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toBeVisible();
	await expect(dock(page).getByRole('button', { name: 'Resume' })).toBeVisible();
	await dock(page).getByRole('button', { name: 'Stop replay' }).click();
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect.poll(async () => (await heard(page)).ended.filter((e) => e.call_id === first.call_id && (e as { how: string }).how === 'stopped').length).toBe(1);
	await expect(dock(page).getByRole('button', { name: 'Pause' })).toBeVisible();

	// Paused by the listener first, live listening stays paused after a replay.
	await dock(page).getByRole('button', { name: 'Pause' }).click();
	const before = (await heard(page)).played.filter((p) => p.call_id === first.call_id).length;
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect.poll(async () => (await heard(page)).played.filter((p) => p.call_id === first.call_id).length, { timeout: 30_000 }).toBe(before + 1);
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect(dock(page).getByRole('button', { name: 'Resume' })).toBeVisible();

	// Resume while replaying stops it too.
	await row.getByRole('button', { name: /^Play: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toBeVisible();
	await dock(page).getByRole('button', { name: 'Resume' }).click();
	await expect(dock(page).locator('.replay')).toHaveCount(0);
	await expect(dock(page).getByRole('button', { name: 'Pause' })).toBeVisible();
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
});

/** A call of 4 s on talkgroup from unit, started age ago, with its recording; resolves with its ID. */
function dated(talkgroup: number, unit: number, age: string): Promise<string> {
	return new Promise((resolve, reject) =>
		execFile(env('RA_WEBFIXTURE'), [env('RA_DATABASE_URL'), 'call-dated', 'Bench', env('RA_RECORDINGS'), String(talkgroup), String(unit), age], { timeout: 30_000 }, (err, out, stderr) =>
			err ? reject(new Error(`${err.message}\n${stderr}`)) : resolve(out.trim())
		)
	);
}

test('@live R46: play from here plays the call, then the later ones oldest first through pages; live calls queue, and play by themselves after', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	// A unit of this browser's own, so the three calls are the only ones it has.
	const unit = 2671 + ['chromium', 'firefox', 'webkit'].indexOf(info.project.name);
	const a = await dated(1001, unit, '3m');
	const b = await dated(1001, unit, '2m');
	const c = await dated(1001, unit, '1m');
	// The run's pages are one call long, so it pages through the route's cursor.
	const asked: URL[] = [];
	await page.route(/\/api\/v1\/recent-calls\?.*order=oldest/, (route) => {
		const u = new URL(route.request().url());
		asked.push(u);
		u.searchParams.set('limit', '1');
		return route.continue({ url: u.toString() });
	});
	// b's recording waits until it's let go, so the dock can be looked at mid-run.
	let release = () => {};
	const held = new Promise<void>((r) => (release = r));
	await page.route(`**/api/v1/calls/${b}/recording`, async (route) => {
		await held;
		await route.continue();
	});

	await listen(page);
	await toRecent(page);
	const toggle = page.getByRole('button', { name: 'Filters' });
	if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
	await page.getByLabel('Unit', { exact: true }).fill(String(unit));
	await page.getByRole('button', { name: 'Show calls' }).click();
	// Newest first: c, b, a. Play from here on a plays up the list.
	await expect(rows(page)).toHaveCount(3);
	expect(await rows(page).evaluateAll((els) => els.map((e) => e.getAttribute('data-call-id')))).toEqual([c, b, a]);
	const aStart = await page.locator(`[data-call-id="${a}"] time`).getAttribute('datetime');
	await page.locator(`[data-call-id="${a}"]`).getByRole('button', { name: /^Play from here: Dispatch North/ }).click();
	await expect(dock(page).locator('.replay')).toContainText('From here: call 1 of');
	await expect(display(page).locator('.np-status')).toHaveText('Paused');

	// A live call meanwhile is queued, not heard.
	const sending = sendCalls([{ unit: 5303, atMs: 200, pcm: seconds(2) }]);
	await expect(queued(page)).toHaveText('1 queued', { timeout: 15_000 });
	await sending;

	// Call 2 of 3, its start time in the dock, its row marked.
	const replay = dock(page).locator('.replay');
	await expect(replay).toContainText('From here: call 2 of 3', { timeout: 20_000 });
	await expect(replay.locator('.when')).toHaveText(`Started ${(await page.locator(`[data-call-id="${b}"] time`).innerText()).trim()}`);
	await expect(page.locator(`[data-call-id="${b}"]`)).toHaveAttribute('data-playing', 'yes');
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
				await expect(replay).toBeVisible();
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
				expect(results.violations, `run playing, ${look} ${theme}, ${width} px`).toEqual([]);
				expect(await page.evaluate(() => document.documentElement.scrollWidth), `${width} px scrolls sideways`).toBeLessThanOrEqual(width);
				if (width === 375) await compactDock(page, `${look} ${theme}`);
				if (shots) {
					await page.mouse.move(0, 0);
					await page.locator(`[data-call-id="${b}"]`).scrollIntoViewIfNeeded();
					await page.screenshot({ path: `${shots}/play-from-here-${look}-${theme}-${width}.png` });
				}
			}
	}
	release();

	// The three in time order, each from its recording, then live listening
	// resumes by itself and the call that queued meanwhile plays.
	await expect(replay).toContainText('From here: call 3 of 3', { timeout: 20_000 });
	await expect(replay).toHaveCount(0, { timeout: 20_000 });
	const replayed = (await heard(page)).first.filter((f) => (f as { replay?: boolean }).replay).map((f) => f.call_id);
	expect(replayed).toEqual([a, b, c]);
	await expect(dock(page).getByRole('button', { name: 'Pause' })).toBeVisible();
	await playedFrom(page, 5303, 'recording', 30_000);

	// Every page came from the public route, with the list's filters, from a's start, oldest first.
	expect(asked.length).toBeGreaterThanOrEqual(3);
	for (const u of asked) {
		expect(u.pathname).toBe('/api/v1/recent-calls');
		expect(u.searchParams.get('unit')).toBe(String(unit));
		expect(u.searchParams.get('talkgroups')).toBe('1001');
		expect(u.searchParams.get('from')).toBe(aStart);
	}
	expect(asked.slice(1).every((u) => u.searchParams.get('cursor'))).toBe(true);
	await stopListening(page);
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
	await waiting(page);
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
