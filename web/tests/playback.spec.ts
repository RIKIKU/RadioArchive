/// <reference types="node" />
// Browser playback (issue #58; SPEC.md P2, R38, R39, R44; A6, A7; ADR 0001,
// 0007, 0011) with the whole stack: the fake recorder sends the plugin's
// PCM to the recorder client, which encodes Opus and forwards it to the
// binary, whose live feed reaches the dock's player. The queue and its
// controls are queue.spec.ts's; what both share is live.ts's.
//
// scripts/ci/e2e-web.sh runs these on their own, one at a time (they share
// the one recorder). Their titles carry @live, which picks them out.
import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { audible, bursts, dock, heard, listen, playedFrom, seconds, sendCalls, tone } from './live.ts';

const paths = ['webcodecs', 'wasm'] as const;

for (const path of paths) {
	test(`@live A7: a live call plays through ${path === 'webcodecs' ? 'WebCodecs' : 'the WASM fallback, with AudioDecoder removed'}`, async ({ page }, info) => {
		await listen(page, path);
		const unit = 5001;
		await sendCalls([{ unit, atMs: 200, pcm: [tone] }]);
		const { first, played } = await playedFrom(page, unit);
		console.log(`A7 [${info.project.name}] ${first.call_id}: ${played.path}, ${played.frames} frames, peak ${played.peak.toFixed(3)}`);
		expect(first.path).toBe(path);
		audible(played, path);
		// What played is in the dock until the next call.
		await expect(dock(page).getByText('Last call: Dispatch North, 5001.')).toBeVisible();
	});

	test(`@live a finished call plays from its recording, through ${path === 'webcodecs' ? 'WebCodecs' : 'the WASM fallback'}`, async ({ page, request }, info) => {
		await listen(page, path);
		const unit = 5002;
		await sendCalls([{ unit, atMs: 200, pcm: [tone] }]);
		const { first } = await playedFrom(page, unit);
		// The recording is in (R20): Ogg/Opus.
		await expect
			.poll(async () => (await request.get(`/api/v1/calls/${first.call_id}/recording`)).status(), { timeout: 15_000 })
			.toBe(200);
		const r = await request.get(`/api/v1/calls/${first.call_id}/recording`);
		expect(r.headers()['content-type']).toContain('ogg');
		await page.getByRole('button', { name: 'Play again: Dispatch North' }).click();
		const again = await playedFrom(page, unit, 'recording');
		console.log(`recording [${info.project.name}] ${first.call_id}: ${again.played.path}, ${again.played.frames} frames, peak ${again.played.peak.toFixed(3)}`);
		expect(again.first.call_id).toBe(first.call_id);
		audible(again.played, path);
		// About a second of audio: every frame of the recording decoded.
		expect(again.played.frames).toBeGreaterThanOrEqual(45);
	});
}

test('@live the feed reconnects after a drop and resumes from the last event ID it saw', async ({ page }) => {
	// Keep the page's sockets, what it sends and the highest event ID it gets.
	await page.addInitScript(() => {
		const w = window as unknown as { sockets: WebSocket[]; sent: { last_event_id?: number }[]; seen: number };
		w.sockets = [];
		w.sent = [];
		w.seen = 0;
		const Native = window.WebSocket;
		window.WebSocket = class extends Native {
			constructor(url: string | URL, protocols?: string | string[]) {
				super(url, protocols);
				w.sockets.push(this);
				this.addEventListener('message', (e) => {
					if (typeof e.data !== 'string') return;
					const m = JSON.parse(e.data) as { event_id?: number };
					if (typeof m.event_id === 'number') w.seen = Math.max(w.seen, m.event_id);
				});
			}
			send(data: string) {
				w.sent.push(JSON.parse(data));
				super.send(data);
			}
		};
	});
	await listen(page, 'webcodecs');
	await sendCalls([{ unit: 5005, atMs: 200, pcm: [bursts] }]);
	await playedFrom(page, 5005);
	const seen = await page.evaluate(() => (window as unknown as { seen: number }).seen);
	expect(seen).toBeGreaterThan(0);

	// The connection drops: a new socket, whose set resumes from that ID.
	await page.evaluate(() => (window as unknown as { sockets: WebSocket[] }).sockets.at(-1)!.close());
	await expect
		.poll(() => page.evaluate(() => (window as unknown as { sockets: WebSocket[]; sent: unknown[] }).sockets.length), { timeout: 15_000 })
		.toBe(2);
	await expect.poll(() => page.evaluate(() => (window as unknown as { sent: unknown[] }).sent.length), { timeout: 15_000 }).toBe(2);
	const sent = await page.evaluate(() => (window as unknown as { sent: { type: string; talkgroups: unknown; last_event_id?: number }[] }).sent);
	expect(sent[1]).toEqual({ type: 'set', talkgroups: [{ id: 1001 }], last_event_id: seen });
	await expect(dock(page).getByText(/^Listening\./)).toBeVisible({ timeout: 15_000 });

	// And the next call plays.
	await sendCalls([{ unit: 5006, atMs: 200, pcm: [bursts] }]);
	await playedFrom(page, 5006);
});

test('@live A6: the delay, first PCM packet to first decoded frame, has p95 under 2 s over 50 calls', async ({ page }, info) => {
	test.setTimeout(5 * 60_000);
	await listen(page, 'webcodecs');
	// One call at a time, each over before the next starts, so none waits
	// behind another in the player.
	const calls = Array.from({ length: 50 }, (_, i) => ({ unit: 5100 + i, atMs: 300 + i * 1500, pcm: [bursts] }));
	const sent = await sendCalls(calls);
	expect(sent.size).toBe(50);
	await expect.poll(async () => new Set((await heard(page)).first.map((f) => f.unit)).size, { timeout: 30_000 }).toBe(50);
	const first = (await heard(page)).first;
	const delays = calls
		.map((c) => {
			const f = first.find((x) => x.unit === c.unit && x.source === 'live');
			return f!.at - sent.get(c.unit)!;
		})
		.sort((a, b) => a - b);
	const pct = (p: number) => delays[Math.ceil((p / 100) * delays.length) - 1];
	console.log(
		`A6 [${info.project.name}] delay over ${delays.length} calls: p50 ${pct(50)} ms, p95 ${pct(95)} ms, min ${delays[0]} ms, max ${delays.at(-1)} ms (budget: p95 under 2000 ms, R38)`
	);
	expect(pct(95)).toBeLessThan(2000);
});

test('@live the dock while a call plays and another waits: axe at phone and desktop widths', async ({ page }) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs');
	for (const width of [375, 1280]) {
		await page.setViewportSize({ width, height: 900 });
		const unit = width === 375 ? 5003 : 5004;
		// A long call, and another that starts while it plays and waits.
		const sending = sendCalls([
			{ unit, atMs: 200, pcm: seconds(6) },
			{ unit: unit + 10, atMs: 1200, pcm: [bursts] }
		]);
		await expect(dock(page).locator('.tg')).toHaveText('Dispatch North', { timeout: 15_000 });
		await expect(dock(page).getByText('1 queued')).toBeVisible({ timeout: 10_000 });
		const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
		expect(results.violations).toEqual([]);
		expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
		await sending;
		await playedFrom(page, unit + 10, 'recording');
	}
});
