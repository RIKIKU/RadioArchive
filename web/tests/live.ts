/// <reference types="node" />
// What the playback tests (@live: playback.spec.ts, queue.spec.ts) share:
// the fake recorder's calls, the player's announcements, and an admin's
// changes, through the admin API. scripts/ci/e2e-web.sh starts the
// recorder client and runs them on their own, one at a time (they share the
// one recorder), naming in the environment the fake recorder
// (RA_FAKERECORDER), its fixtures (RA_FIXTURES), the client's UDP address
// (RA_UDP_ADDR), the folder for the recordings' WAVs (RA_MEDIA), the file
// that holds back trunk-recorder's heartbeats (RA_HEARTBEATS_HELD), and the
// fixture's command (RA_WEBFIXTURE) with its database (RA_DATABASE_URL),
// or the API (RA_URL) for what an admin does.
import { expect, type Page } from '@playwright/test';
import { execFile } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

export function env(name: string): string {
	const v = process.env[name];
	if (!v) throw new Error(`${name} isn't set: run the playback tests through scripts/ci/e2e-web.sh`);
	return v;
}

/** One call for the fake recorder: on talkgroup (1001 if not given), from unit, its audio at atMs. */
export interface PlannedCall {
	unit: number;
	talkgroup?: number;
	/** trunk-recorder's shortName for the system (`ex`, attached to EX, if not given). */
	shortName?: string;
	atMs: number;
	/** Fixture PCM files, played one after another. */
	pcm: { file: string; ms: number }[];
}

let bootID = Math.floor(Math.random() * 2 ** 30);
let callNum = Date.now() * 100;

function run(file: string, args: string[], timeout: number): Promise<string> {
	return new Promise<string>((resolve, reject) =>
		execFile(file, args, { timeout }, (err, stdout, stderr) => (err ? reject(new Error(`${file}: ${err.message}\n${stderr}`)) : resolve(stdout)))
	);
}

/** Has the fake recorder send a scenario, and resolves with what it printed. */
function replay(scenario: object, firstPCM: boolean): Promise<string> {
	const file = join(mkdtempSync(join(tmpdir(), 'ra-scenario-')), 'scenario.json');
	writeFileSync(file, JSON.stringify(scenario));
	const args = ['-scenario', file, '-addr', env('RA_UDP_ADDR'), '-fixtures', env('RA_FIXTURES'), '-recordings', '/'];
	if (firstPCM) args.push('-first-pcm');
	return run(env('RA_FAKERECORDER'), args, 10 * 60_000);
}

const heartbeat = (atMs: number, rate: number) => ({ at_ms: atMs, type: 'heartbeat', systems: [{ sys_num: 0, short_name: 'ex', system_type: 'p25', decode_rate: rate }] });

/**
 * Has the fake recorder send calls, from now, and resolves when it has sent
 * them all with the time each call's first PCM went (by unit), in Unix ms.
 */
export async function sendCalls(calls: PlannedCall[]): Promise<Map<number, number>> {
	const media = env('RA_MEDIA');
	const epoch = Date.now();
	const scenario = {
		boot_id: bootID++,
		epoch_ms: epoch,
		calls: {} as Record<string, object>,
		events: [heartbeat(0, 40)] as object[]
	};
	for (const c of calls) {
		const name = `u${c.unit}`;
		const num = callNum++;
		scenario.calls[name] = { sys_num: 0, call_num: num, start_time: Math.floor((epoch + c.atMs) / 1000), short_name: c.shortName ?? 'ex', talkgroup: c.talkgroup ?? 1001, freq: 851012500 };
		scenario.events.push({ at_ms: c.atMs, type: 'transmission', call: name, unit: c.unit });
		let at = c.atMs;
		for (const p of c.pcm) {
			scenario.events.push({ at_ms: at, type: 'audio', call: name, unit: c.unit, pcm: p.file });
			at += p.ms;
		}
		scenario.events.push({ at_ms: at + 100, type: 'call_end', call: name, filename: `${media}/ex/${num}.wav` });
	}
	const out = await replay(scenario, true);
	const sent = new Map<number, number>();
	for (const line of out.split('\n').filter(Boolean)) {
		const m = JSON.parse(line) as { call: string; sent_at_ms: number };
		sent.set(Number(m.call.slice(1)), m.sent_at_ms);
	}
	return sent;
}

/**
 * trunk-recorder's control channel stops decoding (A27): the background
 * heartbeats are held back and the plugin reports a decode rate of 0 every
 * 2 s, until the returned function is called, which lets the healthy
 * heartbeats go on again.
 */
export function recorderDeaf(): () => void {
	const held = env('RA_HEARTBEATS_HELD');
	writeFileSync(held, '');
	const events = Array.from({ length: 300 }, (_, i) => heartbeat(i * 2000, 0));
	const file = join(mkdtempSync(join(tmpdir(), 'ra-scenario-')), 'deaf.json');
	writeFileSync(file, JSON.stringify({ boot_id: bootID++, epoch_ms: Date.now(), calls: {}, events }));
	const child = execFile(env('RA_FAKERECORDER'), ['-scenario', file, '-addr', env('RA_UDP_ADDR'), '-fixtures', env('RA_FIXTURES')], () => undefined);
	return () => {
		child.kill('SIGINT');
		rmSync(held, { force: true });
	};
}

/**
 * The recorder client's connection drops (A27): the client is stopped while
 * the file RA_CLIENT_HELD exists (scripts/ci/e2e-web.sh), until the
 * returned function is called, which starts it again.
 */
export function clientDisconnected(): () => void {
	const held = env('RA_CLIENT_HELD');
	writeFileSync(held, '');
	return () => rmSync(held, { force: true });
}

/**
 * Changes one of the fixture's talkgroups as an admin does, through the admin API (signed in as the
 * fixture's admin, ADR 0025); resolves with when the change was sent (Unix ms).
 */
export async function setTalkgroup(id: number, field: 'state' | 'delay' | 'alert' | 'led', value: string | number): Promise<number> {
	const out = await run(env('RA_WEBFIXTURE'), [env('RA_URL'), 'talkgroup', String(id), field, String(value)], 30_000);
	return Number(out.trim());
}

export const tone = { file: 'tone-440hz-1s.pcm', ms: 1000 };
export const bursts = { file: 'noise-bursts-500ms.pcm', ms: 500 };
/** n seconds of tone. */
export const seconds = (n: number) => Array.from({ length: n }, () => tone);

export interface FirstFrame {
	call_id: string;
	unit: number | null;
	path: string;
	source: string;
	at: number;
}
export interface Played {
	call_id: string;
	path: string;
	source: string;
	frames: number;
	peak: number;
}
export interface Ended {
	call_id: string;
	how: 'skipped' | 'cleared' | 'withdrawn';
	at: number;
}
export type Heard = {
	first: FirstFrame[];
	played: Played[];
	ended: Ended[];
	alerts: { call_id: string; alert: string }[];
	order: string[];
	/** Previous's presses settling on a call (ADR 0048). */
	previous: { call_id: string; steps: number }[];
};

/**
 * Opens the Listen page with the set given ({1001} if none), collecting
 * what the player announces; without WebCodecs' AudioDecoder when wasm is
 * asked for (A7). Then presses Start listening and waits for the feed to
 * take the set.
 */
export async function listen(page: Page, path: 'webcodecs' | 'wasm' = 'webcodecs', set: { id: number; priority?: boolean }[] = [{ id: 1001 }], playback?: object) {
	await page.addInitScript(
		({ removeDecoder, set, playback }) => {
			if (removeDecoder) delete (window as { AudioDecoder?: unknown }).AudioDecoder;
			localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: set }));
			if (playback) localStorage.setItem('radioarchive.playback', JSON.stringify(playback));
			const heard: Heard = { first: [], played: [], ended: [], alerts: [], order: [], previous: [] };
			addEventListener('radioarchive:previous', (e) => heard.previous.push((e as CustomEvent).detail));
			(window as unknown as { heard: Heard }).heard = heard;
			addEventListener('radioarchive:first-frame', (e) => {
				const d = (e as CustomEvent).detail;
				heard.first.push(d);
				heard.order.push(d.call_id);
			});
			addEventListener('radioarchive:played', (e) => heard.played.push((e as CustomEvent).detail));
			addEventListener('radioarchive:ended', (e) => heard.ended.push((e as CustomEvent).detail));
			addEventListener('radioarchive:alert', (e) => heard.alerts.push((e as CustomEvent).detail));
		},
		{ removeDecoder: path === 'wasm', set, playback }
	);
	await page.goto('/');
	// The set has loaded: the talkgroups on, at the page's foot.
	await expect(page.getByRole('heading', { name: /^Talkgroups on \(/ })).toBeVisible();
	await page.getByRole('button', { name: 'Start listening' }).click();
	await waiting(page);
	await expect(dock(page).locator('.player')).toHaveAttribute('data-decoder', path);
}

/** The frame's dock, "Player": the controls (ADR 0051). */
export const dock = (page: Page) => page.getByRole('region', { name: 'Player' });

/** Presses Stop listening: in the bar, or on a phone in Actions (ADR 0055). */
export async function stopListening(page: Page) {
	const stop = dock(page).getByRole('button', { name: 'Stop listening' });
	if (!(await stop.isVisible())) await dock(page).getByRole('group', { name: 'Player controls' }).getByRole('button', { name: /^More actions/ }).click();
	await stop.click();
}

/**
 * The now-playing display at the top of the page (ADR 0051): the whole of
 * it on the Listen page, a slim line on the others while listening. Its
 * data-state is the status's: idle, connecting, waiting, live, behind,
 * delayed or paused.
 */
export const display = (page: Page) => page.getByRole('region', { name: 'Now playing' });

/** Listening, connected, with nothing playing: the display says Waiting. */
export async function waiting(page: Page, timeout = 15_000) {
	await expect(display(page)).toHaveAttribute('data-state', 'waiting', { timeout });
}

export const heard = (page: Page) => page.evaluate(() => (window as unknown as { heard: Heard }).heard);

/** The first frame of the call heard from unit (source given, or any). */
export async function firstOf(page: Page, unit: number, source?: string, timeout = 30_000): Promise<FirstFrame> {
	let out: FirstFrame | undefined;
	await expect
		.poll(
			async () => {
				out = (await heard(page)).first.find((f) => f.unit === unit && (!source || f.source === source));
				return !!out;
			},
			{ timeout, intervals: [100] }
		)
		.toBe(true);
	return out!;
}

/** Waits until the call heard from unit has finished playing, and returns how. */
export async function playedFrom(page: Page, unit: number, source = 'live', timeout = 30_000): Promise<{ first: FirstFrame; played: Played }> {
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
export function audible(p: Played, path: string) {
	expect(p.path, 'the decoder that ran').toBe(path);
	expect(p.frames, 'decoded frames').toBeGreaterThan(10);
	expect(p.peak, "the output's loudest sample").toBeGreaterThan(0.02);
}
