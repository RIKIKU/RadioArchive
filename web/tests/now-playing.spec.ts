/// <reference types="node" />
// The now-playing display (SPEC.md R45, R113; A43; ADR 0051) with the whole
// stack: the fake recorder's calls through the recorder client and the
// binary's live feed. At the top of the Listen page, idle too, every field
// in a fixed place at a fixed height; a slim line on the other pages while
// listening; the last calls heard under it, a tap playing one again; the
// dock holding only the controls, its height steady.
//
// scripts/ci/e2e-web.sh runs these with the playback tests, one at a time.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { display, dock, firstOf, heard, listen, playedFrom, seconds, sendCalls, tone, waiting } from './live.ts';

const height = async (page: Page, what = display(page)) => (await what.boundingBox())!.height;
const box = async (page: Page, cls: string) => (await display(page).locator(cls).boundingBox())!;

/** Where each field is: they never move between calls or states. */
async function places(page: Page) {
	const out: Record<string, { x: number; y: number }> = {};
	for (const f of ['.np-status', '.where', '.started', '.name', '.tg', '.unit', '.elapsed', '.queued', '.priority', '.hold']) {
		const b = await box(page, f);
		out[f] = { x: Math.round(f === '.np-status' || f === '.name' ? b.x : b.x + b.width), y: Math.round(b.y) };
	}
	return out;
}

/** A call's start time as the API stored it, in the deployment's zone (the test API runs in Australia/Melbourne). */
async function startedOf(page: Page, callId: string) {
	let started = '';
	await expect
		.poll(
			async () => {
				const r = await page.request.get('/api/v1/recent-calls?talkgroups=1001,1002');
				const c = ((await r.json()) as { calls: { id: string; started_at: string }[] }).calls.find((x) => x.id === callId);
				started = c?.started_at ?? '';
				return !!started;
			},
			{ timeout: 15_000 }
		)
		.toBe(true);
	const zone = ((await (await page.request.get('/api/v1/catalogue')).json()) as { time_zone: string }).time_zone;
	return new Intl.DateTimeFormat('en-AU', { timeZone: zone, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(started));
}

async function axe(page: Page, what: string) {
	for (const theme of ['light', 'dark']) {
		await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
		const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
		expect(results.violations, `${what}, ${theme}`).toEqual([]);
		const width = page.viewportSize()!.width;
		expect(await page.evaluate(() => document.documentElement.scrollWidth), `${what}: no sideways scroll`).toBeLessThanOrEqual(width);
	}
}

for (const width of [1280, 375] as const)
	test(`@live R113, A43: the display keeps its height and its fields' places through every state, and the dock its height, ${width}px`, async ({ page }) => {
		test.setTimeout(3 * 60_000);
		await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
		await page.addInitScript(() => localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }, { id: 1002, priority: true }] })));
		await page.goto('/');
		await expect(display(page)).toHaveAttribute('data-state', 'idle');
		const idle = await height(page);
		expect(idle).toBeGreaterThan(0);

		await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002, priority: true }]);
		expect(await height(page), 'waiting').toBeCloseTo(idle, 0);
		const docked = await height(page, dock(page));
		await expect(dock(page).getByRole('button', { name: 'Catch up to live' })).toBeDisabled();
		const empty = await places(page);

		// A long call from a unit with an alias, and one on priority 1002 queued behind it.
		const sending = sendCalls([
			{ unit: 5001, atMs: 200, pcm: seconds(10) },
			{ unit: 5291, talkgroup: 1002, atMs: 1500, pcm: [tone] }
		]);
		const live = await firstOf(page, 5001, 'live');
		await expect(display(page)).toHaveAttribute('data-state', 'live');
		await expect(display(page).locator('.name')).toHaveText('Dispatch North');
		// The unit by its alias, never its ID as well (R27).
		await expect(display(page).locator('.unit')).toHaveText('Unit Engine 1');
		await expect(display(page)).not.toContainText('5001');
		await expect(display(page).locator('.priority')).toHaveText('☆ Not priority');
		expect(await height(page), 'live').toBeCloseTo(idle, 0);
		expect(await places(page), 'live: every field where it was').toEqual(empty);

		// The call's start time, not a clock: it holds while the call plays on.
		const started = await startedOf(page, live.call_id);
		await expect(display(page).locator('.started .value')).toHaveText(`Started ${started}`);
		const elapsed = await display(page).locator('.elapsed .value').innerText();
		await page.waitForTimeout(2_100);
		await expect(display(page).locator('.started .value')).toHaveText(`Started ${started}`);
		expect(await display(page).locator('.elapsed .value').innerText(), 'the duration so far ticks').not.toBe(elapsed);

		// Queued: Catch up enabled in place, and the dock no taller.
		await expect(display(page).locator('.queued')).toHaveText('1 queued', { timeout: 10_000 });
		await expect(dock(page).getByRole('button', { name: 'Catch up to live' })).toBeEnabled();
		expect(await height(page, dock(page)), 'the dock while behind').toBeCloseTo(docked, 0);

		// Paused, then behind.
		await dock(page).getByRole('button', { name: 'Pause' }).click();
		await expect(display(page)).toHaveAttribute('data-state', 'paused');
		await expect(display(page).locator('.np-status')).toHaveText('Paused');
		expect(await height(page), 'paused').toBeCloseTo(idle, 0);
		await page.waitForTimeout(6_000);
		await dock(page).getByRole('button', { name: 'Resume' }).click();
		await expect(display(page)).toHaveAttribute('data-state', 'behind');
		await expect(display(page).locator('.np-status')).toHaveText(/^Behind \d+ s$/);
		expect(await height(page), 'behind').toBeCloseTo(idle, 0);
		expect(await places(page), 'behind: every field where it was').toEqual(empty);
		expect(await height(page, dock(page)), 'the dock while behind').toBeCloseTo(docked, 0);
		await axe(page, `the display behind, ${width} px`);
		await sending;

		// The priority call, from a unit known by its ID only.
		await firstOf(page, 5291);
		await expect(display(page).locator('.name')).toHaveText('Fireground 41');
		await expect(display(page).locator('.unit')).toHaveText('Unit 5291');
		await expect(display(page).locator('.priority')).toHaveText('★ Priority');
		expect(await height(page), 'another call').toBeCloseTo(idle, 0);
		await playedFrom(page, 5291, 'recording');
		await waiting(page);
		// The last call stays on the display, with its duration.
		await expect(display(page).locator('.name')).toHaveText('Fireground 41');
		await expect(display(page).locator('.elapsed .value')).toHaveText(/^Duration \d:\d\d$/);
		expect(await height(page), 'waiting, the last call shown').toBeCloseTo(idle, 0);
		expect(await places(page), 'waiting: every field where it was').toEqual(empty);
		expect(await height(page, dock(page)), 'the dock, waiting').toBeCloseTo(docked, 0);
		await dock(page).getByRole('button', { name: 'Stop listening' }).click();
		await expect(display(page)).toHaveAttribute('data-state', 'idle');
		expect(await height(page), 'idle again').toBeCloseTo(idle, 0);
	});

test('@live R113: the display stays in view under the top bar while the page scrolls, and the page scrolls a focused control out from under it', async ({ page }) => {
	await page.setViewportSize({ width: 375, height: 640 });
	await listen(page);
	const top = (await page.locator('header.top').boundingBox())!;
	await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
	// At the frame's --topbar-height, its measured inner height: the bar's own border may overlap it by a pixel.
	await expect.poll(async () => Math.abs((await display(page).boundingBox())!.y - (top.y + top.height))).toBeLessThanOrEqual(1.5);
	// The page's scroll padding covers the top bar and the display.
	const padding = await page.evaluate(() => parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop));
	const d = (await display(page).boundingBox())!;
	expect(padding).toBeGreaterThanOrEqual(d.y + d.height - 1);
	// Stopped and gone to another page, then back: no padding left behind on the way.
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
	await page.getByRole('navigation').getByRole('link', { name: /^Recent/ }).filter({ visible: true }).first().click();
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
	expect(await page.evaluate(() => document.documentElement.style.scrollPaddingTop)).toBe('');
});

test('@live R113: the other pages show a slim line while listening, at a fixed height, and none when stopped', async ({ page }) => {
	test.setTimeout(4 * 60_000);
	await listen(page);
	await page.locator('nav.side').getByRole('link', { name: 'Recent calls' }).click();
	await expect(page.getByRole('heading', { name: 'Recent calls' })).toBeVisible();
	const slim = display(page);
	await expect(slim).toHaveAttribute('data-variant', 'slim');
	await waiting(page);
	await expect(slim.locator('.name')).toHaveText('—');
	const h = await height(page);
	const sending = sendCalls([{ unit: 5292, atMs: 200, pcm: seconds(4) }]);
	const live = await firstOf(page, 5292, 'live');
	await expect(slim).toHaveAttribute('data-state', 'live');
	await expect(slim.locator('.name')).toHaveText('Dispatch North');
	await expect(slim.locator('.np-status')).toHaveText('Live');
	await expect(slim.locator('.started .value')).toHaveText(`Started ${await startedOf(page, live.call_id)}`);
	expect(await height(page), 'the slim line playing').toBeCloseTo(h, 0);
	// One line.
	const name = (await slim.locator('.name').boundingBox())!;
	expect(name.height).toBeLessThan(h);
	for (const width of [1280, 375]) {
		await page.setViewportSize({ width, height: 800 });
		await axe(page, `the slim line, ${width} px`);
		const line = (await slim.boundingBox())!;
		for (const f of ['.name', '.np-status', '.started']) {
			const b = (await slim.locator(f).boundingBox())!;
			expect(b.y, `${f} in the line, ${width} px`).toBeGreaterThanOrEqual(line.y - 0.5);
			expect(b.y + b.height, `${f} in the line, ${width} px`).toBeLessThanOrEqual(line.y + line.height + 0.5);
		}
	}
	await sending;
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
	await expect(slim).toHaveCount(0);
});

test('@live R113: the last calls heard sit under the display; a tap plays one again, then the calls after it, as Previous does', async ({ page }) => {
	test.setTimeout(3 * 60_000);
	await page.setViewportSize({ width: 1280, height: 900 });
	await listen(page, 'webcodecs', [{ id: 1001 }, { id: 1002 }]);
	const rows = page.getByRole('list', { name: 'Recent calls heard' });
	// While listening: four places, "—" until filled, so a call coming moves nothing.
	await expect(rows.getByRole('listitem')).toHaveCount(4);
	await expect(rows.getByRole('button')).toHaveCount(0);
	const heading = (await page.getByRole('heading', { level: 1, name: 'Listen' }).boundingBox())!.y;

	await sendCalls([
		{ unit: 5801, atMs: 200, pcm: [tone] },
		{ unit: 5802, talkgroup: 1002, atMs: 2500, pcm: [tone] },
		{ unit: 5803, atMs: 5000, pcm: [tone] }
	]);
	for (const u of [5801, 5802, 5803]) await playedFrom(page, u, 'live');
	await waiting(page);
	// Newest first, each with its start time.
	await expect(rows.getByRole('button')).toHaveText([/^Play again: Dispatch North, started \d\d:\d\d:\d\d$/, /^Play again: Fireground 41, started /, /^Play again: Dispatch North, started /]);
	expect((await page.getByRole('heading', { level: 1, name: 'Listen' }).boundingBox())!.y, 'nothing moved').toBeCloseTo(heading, 0);
	await axe(page, 'the recent calls heard');

	// A tap on the second: it plays again, from its recording, then the one after it.
	const second = (await heard(page)).first.find((f) => f.unit === 5802)!;
	await rows.getByRole('button').nth(1).click();
	await expect.poll(async () => (await heard(page)).previous.map((p) => p.call_id)).toContain(second.call_id);
	await playedFrom(page, 5802, 'recording');
	await playedFrom(page, 5803, 'recording');

	// A phone shows three of them.
	await page.setViewportSize({ width: 375, height: 812 });
	await expect(rows.getByRole('listitem').nth(3)).toBeHidden();
	await expect(rows.getByRole('listitem').nth(2)).toBeVisible();
	await axe(page, 'the recent calls heard on a phone');
	await dock(page).getByRole('button', { name: 'Stop listening' }).click();
	await expect(rows).toHaveCount(0);
});
