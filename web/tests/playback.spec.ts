/// <reference types="node" />
// Browser playback (issue #58; SPEC.md P2, R38, R39, R44; A6, A7; ADR 0001,
// 0007, 0011) with the whole stack: the fake recorder sends the plugin's
// PCM to the recorder client, which encodes Opus and forwards it to the
// binary, whose live feed reaches the Listen page's player.
//
// scripts/ci/e2e-web.sh starts the client and runs these on their own, one
// at a time (they share the one recorder), naming in the environment the
// fake recorder (RA_FAKERECORDER), its fixtures (RA_FIXTURES), the client's
// UDP address (RA_UDP_ADDR) and the folder for the recordings' WAVs
// (RA_MEDIA). Their titles carry @live, which picks them out.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { execFile } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

function env(name: string): string {
	const v = process.env[name];
	if (!v) throw new Error(`${name} isn't set: run the playback tests through scripts/ci/e2e-web.sh`);
	return v;
}

/** One call for the fake recorder: on 1001, from unit, its audio at atMs. */
interface PlannedCall {
	unit: number;
	atMs: number;
	/** Fixture PCM files, played one after another. */
	pcm: { file: string; ms: number }[];
}

let bootID = Math.floor(Math.random() * 2 ** 31);
let callNum = Date.now() * 100;

/**
 * Has the fake recorder send calls, from now, and resolves when it has sent
 * them all with the time each call's first PCM went (by unit), in Unix ms.
 */
async function sendCalls(calls: PlannedCall[]): Promise<Map<number, number>> {
	const media = env('RA_MEDIA');
	const epoch = Date.now();
	const scenario = {
		boot_id: bootID++,
		epoch_ms: epoch,
		calls: {} as Record<string, object>,
		events: [{ at_ms: 0, type: 'heartbeat', systems: [{ sys_num: 0, short_name: 'ex', system_type: 'p25', decode_rate: 40 }] }] as object[]
	};
	for (const c of calls) {
		const name = `u${c.unit}`;
		const num = callNum++;
		scenario.calls[name] = { sys_num: 0, call_num: num, start_time: Math.floor((epoch + c.atMs) / 1000), short_name: 'ex', talkgroup: 1001, freq: 851012500 };
		scenario.events.push({ at_ms: c.atMs, type: 'transmission', call: name, unit: c.unit });
		let at = c.atMs;
		for (const p of c.pcm) {
			scenario.events.push({ at_ms: at, type: 'audio', call: name, unit: c.unit, pcm: p.file });
			at += p.ms;
		}
		scenario.events.push({ at_ms: at + 100, type: 'call_end', call: name, filename: `${media}/ex/${num}.wav` });
	}
	const file = join(mkdtempSync(join(tmpdir(), 'ra-scenario-')), 'scenario.json');
	writeFileSync(file, JSON.stringify(scenario));
	const out = await new Promise<string>((resolve, reject) =>
		execFile(
			env('RA_FAKERECORDER'),
			['-scenario', file, '-addr', env('RA_UDP_ADDR'), '-fixtures', env('RA_FIXTURES'), '-recordings', '/', '-first-pcm'],
			{ timeout: 10 * 60_000 },
			(err, stdout, stderr) => (err ? reject(new Error(`fakerecorder: ${err.message}\n${stderr}`)) : resolve(stdout))
		)
	);
	const sent = new Map<number, number>();
	for (const line of out.split('\n').filter(Boolean)) {
		const m = JSON.parse(line) as { call: string; sent_at_ms: number };
		sent.set(Number(m.call.slice(1)), m.sent_at_ms);
	}
	return sent;
}

const tone = { file: 'tone-440hz-1s.pcm', ms: 1000 };
const bursts = { file: 'noise-bursts-500ms.pcm', ms: 500 };

interface FirstFrame {
	call_id: string;
	unit: number | null;
	path: string;
	source: string;
	at: number;
}
interface Played {
	call_id: string;
	path: string;
	source: string;
	frames: number;
	peak: number;
}
type Heard = { first: FirstFrame[]; played: Played[] };

/**
 * Opens the Listen page with {1001}, collecting what the player announces;
 * without WebCodecs' AudioDecoder when wasm is asked for (A7). Then
 * presses Start listening and waits for the feed to take the set.
 */
async function listen(page: Page, path: 'webcodecs' | 'wasm') {
	await page.addInitScript((removeDecoder) => {
		if (removeDecoder) delete (window as { AudioDecoder?: unknown }).AudioDecoder;
		localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001 }] }));
		const heard: Heard = { first: [], played: [] };
		(window as unknown as { heard: Heard }).heard = heard;
		addEventListener('radioarchive:first-frame', (e) => heard.first.push((e as CustomEvent).detail));
		addEventListener('radioarchive:played', (e) => heard.played.push((e as CustomEvent).detail));
	}, path === 'wasm');
	await page.goto('/');
	await expect(page.getByRole('list', { name: 'Your listening set' })).toContainText('Dispatch North');
	await page.getByRole('button', { name: 'Start listening' }).click();
	await expect(page.getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });
	await expect(page.locator('section.player')).toHaveAttribute('data-decoder', path);
}

const heard = (page: Page) => page.evaluate(() => (window as unknown as { heard: Heard }).heard);

/** Waits until the call heard from unit has finished playing, and returns how. */
async function playedFrom(page: Page, unit: number, source = 'live', timeout = 30_000): Promise<{ first: FirstFrame; played: Played }> {
	let out: { first: FirstFrame; played: Played } | undefined;
	await expect
		.poll(
			async () => {
				const h = await heard(page);
				const first = h.first.find((f) => f.unit === unit && f.source === source);
				const played = first && h.played.find((p) => p.call_id === first.call_id && p.source === source);
				if (first && played) out = { first, played };
				return !!out;
			},
			{ timeout, intervals: [100] }
		)
		.toBe(true);
	return out!;
}

/** Decoded audio that wasn't silence reached the output: the meter is after every node but the speakers. */
function audible(p: Played, path: string) {
	expect(p.path, 'the decoder that ran').toBe(path);
	expect(p.frames, 'decoded frames').toBeGreaterThan(10);
	expect(p.peak, "the output's loudest sample").toBeGreaterThan(0.02);
}

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
		// What played is on the page until the next call.
		await expect(page.getByText('Last call: Dispatch North, Unit 5001')).toBeVisible();
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
	await expect(page.getByText('Listening. Calls play here as they come in.')).toBeVisible({ timeout: 15_000 });

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

test('@live the player while a call plays: axe at phone and desktop widths, and the screenshots', async ({ page }, info) => {
	test.setTimeout(2 * 60_000);
	await listen(page, 'webcodecs');
	for (const width of [375, 1280]) {
		await page.setViewportSize({ width, height: 900 });
		const unit = width === 375 ? 5003 : 5004;
		// A long call, and another that starts while it plays and waits.
		const sending = sendCalls([
			{ unit, atMs: 200, pcm: [tone, tone, tone, tone, tone, tone] },
			{ unit: unit + 10, atMs: 1200, pcm: [bursts] }
		]);
		await expect(page.locator('section.player .np-name')).toHaveText('Dispatch North', { timeout: 15_000 });
		await expect(page.getByText('1 call waiting')).toBeVisible({ timeout: 10_000 });
		const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
		expect(results.violations).toEqual([]);
		expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
		if (process.env.RA_SHOTS && info.project.name === 'chromium') {
			await page.mouse.move(0, 0);
			const height = await page.evaluate(() => document.documentElement.scrollHeight);
			await page.setViewportSize({ width, height: Math.max(height, 900) });
			await page.screenshot({ path: `${process.env.RA_SHOTS}/playing-${width}.png` });
			await page.setViewportSize({ width, height: 900 });
		}
		await sending;
		await playedFrom(page, unit + 10);
	}
});
