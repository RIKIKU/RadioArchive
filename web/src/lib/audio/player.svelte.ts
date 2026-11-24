// The player (SPEC.md P2, R22, R38, R39, R43, R44, R45; ADR 0011, 0012):
// the live feed's calls, decoded (decoder.ts) and played into Web Audio,
// one at a time, and a finished call played from its recording through the
// same output. The dock (Dock.svelte) shows it and holds its controls.
//
// The queue (R43; the rules are queue.ts's):
// - One call plays at a time; the rest wait in order of start time. A
//   priority call goes first, but never cuts off the call playing.
// - A queued call that's still live at its turn plays from its start, from
//   the frames heard while it waited, and goes on live: "behind by N s"
//   until Jump to live. One that has ended by its turn plays from its
//   recording.
// - Nothing is dropped automatically. A withdrawn call (R31) leaves the
//   queue, and stops at once if it's playing.
//
// The controls (R44): nothing queues before Start listening; Skip ends the
// call playing; Jump to live clears the queue and brings the call playing to
// the live edge; Pause holds the output and keeps queuing, and Resume
// carries on through the queue; Hold hears only the playing call's
// talkgroup until it's switched off, and never touches the saved set.
//
// For the browser tests, and anything else that wants to know, the player
// announces on the window:
//
//   radioarchive:first-frame  {call_id, unit, path, source, at}       a call's first decoded frame (at: Unix ms)
//   radioarchive:played       {call_id, path, source, frames, peak}   a call has finished playing
//   radioarchive:ended        {call_id, how, at}                      a call stopped or left the queue unplayed:
//                                                                     how is skipped, cleared (Jump to live) or withdrawn
//   radioarchive:alert        {call_id, alert}                        a talkgroup's alert sound played (R22)

import { attachFeed } from '#lib/live/listening-set.ts';
import { LiveFeed, type CallEvent, type EventTalkgroup, type NoticeKind } from '#lib/live/feed.ts';
import { decoding, type Decoding, type DecodePath, type OpusStream, type Pcm } from './decoder.ts';
import { readOggOpus } from './ogg.ts';
import { heldThrough, nextIndex, type TalkgroupKey } from './queue.ts';
import { playAlert } from './alerts.ts';
import { Replay } from './replay.svelte.ts';
import { LIVE_WITHIN } from 'archiveux/live.js';

export type Source = 'live' | 'recording';

/** What the dock shows of a call. */
export interface CallInfo {
	callId: string;
	talkgroup: EventTalkgroup;
	patched?: number[];
	/** Present only when the deployment has several systems (R15). */
	system?: { id: number; label: string; name: string };
	unit: { id: number; name: string } | null;
	startedAt: string;
	source: Source;
	/** A delayed talkgroup's call (R23). */
	delayMinutes?: number;
	/** Once it has ended. */
	durationMs?: number;
}

interface Call {
	info: CallInfo;
	ended: boolean;
	recording?: string;
	/** Asked for again (Play again): plays before everything else. */
	first?: boolean;
	/** Frames heard before its turn. */
	packets: { seq: number; data: Uint8Array }[];
	stream?: OpusStream;
	firstFrame: boolean;
	frames: number;
	peak: number;
	/** When its first frame plays, and when its scheduled audio ends, in the output's time. */
	startsAt?: number;
	endsAt: number;
	sources: AudioBufferSourceNode[];
	finishing?: boolean;
	done?: boolean;
}

/** Where the call playing is, for the dock: updated a few times a second. */
export interface Position {
	/** Seconds behind the live edge (R45); 0 at it. */
	behind: number;
	/** The call's duration so far, in seconds. */
	elapsed: number;
}

export interface PlayerOptions {
	/** Whether a talkgroup is marked priority in the listening set (R43). */
	isPriority?(t: TalkgroupKey): boolean;
	/** Whether alert sounds are on in the playback settings (R48). */
	alerts?(): boolean;
}

/** How far ahead of now the first frame of a run is scheduled: room for the network's jitter. */
const LEAD = 0.15;
/** How many times a recording is asked for, a second apart, before it's skipped. */
const RECORDING_TRIES = 5;
/** How long a call that has left is remembered, so its later events and frames are ignored. */
const FORGET_AFTER_MS = 10 * 60_000;

const recordingURL = (id: string) => `/api/v1/calls/${encodeURIComponent(id)}/recording`;

function announce(name: string, detail: object) {
	window.dispatchEvent(new CustomEvent(name, { detail }));
}

/** A call's info as the queue rules read it. */
const ruled = (c: Call) => ({ ...c.info, first: c.first });

export class Player {
	/** stopped, starting (the gesture's been made), or listening. */
	phase: 'stopped' | 'starting' | 'listening' = $state('stopped');
	/** Whether the live feed is connected now. */
	connected = $state(false);
	/** Which decoder plays (R39). */
	path: DecodePath | undefined = $state();
	/** The call playing. */
	playing: CallInfo | undefined = $state();
	/** Where it is. */
	position: Position = $state({ behind: 0, elapsed: 0 });
	/** Calls waiting their turn. */
	queued = $state(0);
	/** Of those, how many hold is keeping back. */
	heldBack = $state(0);
	/** Paused: nothing is heard, and calls keep queuing (R44). */
	paused = $state(false);
	/** Hold: only this talkgroup's calls are heard until it's switched off (R44). */
	hold: { talkgroup: EventTalkgroup; key: TalkgroupKey } | undefined = $state();
	/** The feed notices that are on (R15, R71). */
	notices: Record<NoticeKind, boolean> = $state({ recorders: false, radio_full: false });
	/** The last call heard to its end, to play again from its recording. */
	last: CallInfo | undefined = $state();
	/** Why listening couldn't start, if it couldn't. */
	failure = $state('');
	/** A call replayed from recent calls (R46), apart from the queue. */
	readonly replay = new Replay();

	#opts: PlayerOptions;
	#ctx: AudioContext | undefined;
	#out: GainNode | undefined;
	#alertOut: GainNode | undefined;
	#meter: AnalyserNode | undefined;
	#meterBuf: Float32Array<ArrayBuffer> | undefined;
	#meterTimer: ReturnType<typeof setInterval> | undefined;
	#clockTimer: ReturnType<typeof setInterval> | undefined;
	#dec: Decoding | undefined;
	#feed: LiveFeed | undefined;
	/** Every call queued or playing, by ID. */
	#calls = new Map<string, Call>();
	#queue: Call[] = [];
	#current: Call | undefined;
	/** Calls that have left (played, skipped, cleared, withdrawn or held out), and when. */
	#gone = new Map<string, number>();

	constructor(opts: PlayerOptions = {}) {
		this.#opts = opts;
	}

	/**
	 * Starts listening: call it from the Start listening button's click
	 * (R44), since the browser lets audio start only from a gesture.
	 */
	async start() {
		if (this.phase !== 'stopped') return;
		// Live listening and a replay never play over each other.
		this.replay.stop();
		this.phase = 'starting';
		this.failure = '';
		// Made and resumed inside the gesture, before any await.
		const ctx = new AudioContext({ latencyHint: 'interactive' });
		const resumed = ctx.resume().catch(() => undefined);
		this.#ctx = ctx;
		this.#out = ctx.createGain();
		this.#meter = ctx.createAnalyser();
		this.#meter.fftSize = 2048;
		this.#meterBuf = new Float32Array(this.#meter.fftSize);
		this.#out.connect(this.#meter);
		this.#meter.connect(ctx.destination);
		// Alerts go to the speakers beside the calls, not through the meter,
		// so they never count as a call's audio.
		this.#alertOut = ctx.createGain();
		this.#alertOut.connect(ctx.destination);
		try {
			this.#dec = await decoding();
		} catch {
			this.failure = "This browser can't play the radio.";
			this.stop();
			return;
		}
		await resumed;
		if (this.phase !== 'starting') return;
		this.path = this.#dec.path;
		this.phase = 'listening';
		// What reaches the output: the loudest sample of each call (A7).
		this.#meterTimer = setInterval(() => this.#measure(), 20);
		this.#clockTimer = setInterval(() => this.#tick(), 250);
		const feed = new LiveFeed({
			event: (e) => this.#event(e),
			withdrawn: (id) => this.#withdraw(id),
			frame: (id, seq, p) => this.#frame(id, seq, p),
			connected: (on) => (this.connected = on),
			notice: (kind, on) => {
				if (this.notices[kind] !== on) this.notices = { ...this.notices, [kind]: on };
			}
		});
		this.#feed = feed;
		feed.start(attachFeed(feed));
	}

	/** Stops listening: the feed closes and the audio stops. */
	stop() {
		attachFeed(undefined);
		this.#feed?.stop();
		this.#feed = undefined;
		clearInterval(this.#meterTimer);
		clearInterval(this.#clockTimer);
		for (const c of this.#calls.values()) this.#silence(c);
		if (this.#current && !this.#current.done) this.#silence(this.#current);
		this.#calls.clear();
		this.#gone.clear();
		this.#queue = [];
		this.#current = undefined;
		this.#ctx?.close().catch(() => undefined);
		this.#ctx = undefined;
		this.playing = undefined;
		this.position = { behind: 0, elapsed: 0 };
		this.queued = 0;
		this.heldBack = 0;
		this.paused = false;
		this.hold = undefined;
		this.notices = { recorders: false, radio_full: false };
		this.connected = false;
		this.phase = 'stopped';
	}

	/** Skip (R44): ends the call playing; the next one starts. */
	skip() {
		const c = this.#current;
		if (!c) return;
		this.#leave(c, 'skipped');
		this.#current = undefined;
		this.playing = undefined;
		this.#pump();
	}

	/**
	 * Jump to live (R44): clears the queue. A live call playing behind
	 * carries on from the live edge; a recording playing stops. Paused, it
	 * resumes.
	 */
	jumpToLive() {
		if (this.phase !== 'listening') return;
		for (const c of this.#queue) this.#leave(c, 'cleared');
		this.#queue = [];
		const c = this.#current;
		if (c && c.info.source === 'recording') {
			this.#leave(c, 'cleared');
			this.#current = undefined;
			this.playing = undefined;
		} else if (c && this.#ctx && c.endsAt - this.#ctx.currentTime - LEAD >= LIVE_WITHIN) {
			// A live call playing behind: what was scheduled ahead is dropped,
			// and the next frame plays at once. One that has ended finishes.
			this.#stopSources(c);
			c.endsAt = 0;
		}
		if (this.paused) void this.resume();
		this.#pump();
		this.#tick();
	}

	/** Pause (R44): nothing is heard; calls keep queuing, and the call playing keeps its frames. */
	async pause() {
		if (this.phase !== 'listening' || this.paused) return;
		this.paused = true;
		await this.#ctx?.suspend().catch(() => undefined);
	}

	/** Resume: carries on where it paused, then through the queue. A call being replayed stops. */
	async resume() {
		if (!this.paused) return;
		this.replay.stop();
		this.paused = false;
		await this.#ctx?.resume().catch(() => undefined);
		this.#pump();
	}

	/**
	 * Hold (R44): hears only the playing call's talkgroup until switched off.
	 * It's the player's alone: the listening set, saved or sent, is unchanged.
	 */
	setHold(on: boolean) {
		if (!on) {
			this.hold = undefined;
			this.#pump();
			return;
		}
		const c = this.#current;
		if (!c || this.hold) return;
		const t = c.info.talkgroup;
		this.hold = { talkgroup: t, key: c.info.system ? { system: c.info.system.id, id: t.id } : { id: t.id } };
		this.#count();
	}

	/**
	 * Replays a call from recent calls (R46), from its recording: call it
	 * from the press. Live listening pauses, so live calls keep queuing,
	 * and stays paused once the call has played, until the listener
	 * resumes it (ADR 0016; #75's open question 3 may change that).
	 */
	replayCall(info: CallInfo, recording: string) {
		if (this.phase !== 'stopped' && !this.paused) void this.pause();
		this.replay.play(info, recording);
	}

	/**
	 * Replays calls in order, one after another: a basket conversation's
	 * Play all (R64; ADR 0018). Live listening pauses as for one call.
	 */
	replayAll(name: string, calls: { info: CallInfo; url: string }[]) {
		if (!calls.length) return;
		if (this.phase !== 'stopped' && !this.paused) void this.pause();
		this.replay.playAll(name, calls);
	}

	/** Plays a finished call again, from its recording (R20), after the one playing. */
	playAgain(info: CallInfo) {
		if (this.phase !== 'listening') return;
		const c = this.#newCall({ ...info, source: 'recording', delayMinutes: undefined }, true, recordingURL(info.callId));
		c.first = true;
		this.#queue.push(c);
		this.#pump();
	}

	#newCall(info: CallInfo, ended: boolean, recording?: string): Call {
		return { info, ended, recording, packets: [], firstFrame: false, frames: 0, peak: 0, endsAt: 0, sources: [] };
	}

	#infoOf(e: CallEvent, source: Source): CallInfo {
		return {
			callId: e.call_id,
			talkgroup: e.talkgroup,
			patched: e.patched_talkgroups,
			system: e.system,
			unit: e.unit,
			startedAt: e.started_at,
			source,
			delayMinutes: e.delay_minutes,
			durationMs: e.duration_ms
		};
	}

	/** Whether a call is a priority one: any talkgroup of its patch marked so (R43). */
	#priority = (c: { talkgroup: { id: number }; patched?: number[]; system?: { id: number } }): boolean => {
		const is = this.#opts.isPriority;
		if (!is) return false;
		const system = c.system?.id;
		return [c.talkgroup.id, ...(c.patched ?? [])].some((id) => is(system === undefined ? { id } : { system, id }));
	};

	/**
	 * A new call joins the queue, unless hold keeps it out (A9). A call that
	 * starts (live, or released from its delay) sounds its talkgroup's alert
	 * (R22); one missed while away doesn't.
	 */
	#enqueue(info: CallInfo, ended: boolean, recording?: string, starts = true) {
		if (!heldThrough(info, this.hold?.key)) {
			this.#forget(info.callId);
			return;
		}
		const c = this.#newCall(info, ended, recording);
		this.#calls.set(info.callId, c);
		this.#queue.push(c);
		if (starts) this.#alert(c);
		this.#pump();
	}

	#event(e: CallEvent) {
		if (this.#gone.has(e.call_id)) return;
		const c = this.#calls.get(e.call_id);
		switch (e.type) {
			case 'call_start':
				if (!c) this.#enqueue(this.#infoOf(e, 'live'), false);
				return;
			case 'transmission':
				if (!c) return;
				c.info.unit = e.unit;
				c.info.patched = e.patched_talkgroups ?? c.info.patched;
				if (c === this.#current) this.playing = { ...c.info };
				return;
			case 'call_end':
				if (!c) {
					// Missed while away (a resume, R37): played from its recording.
					this.#enqueue(this.#infoOf(e, 'recording'), true, recordingURL(e.call_id), false);
					return;
				}
				c.ended = true;
				c.info.durationMs = e.duration_ms;
				c.info.patched = e.patched_talkgroups ?? c.info.patched;
				if (c === this.#current) {
					this.playing = { ...c.info };
					this.#finish(c);
				}
				return;
			case 'call_delayed':
				// A delayed talkgroup's call (R23): queued, from its recording.
				if (!c) this.#enqueue(this.#infoOf(e, 'recording'), true, e.recording?.url ?? recordingURL(e.call_id));
				return;
		}
	}

	#withdraw(id: string) {
		const c = this.#calls.get(id);
		if (!c) {
			this.#forget(id);
			return;
		}
		this.#leave(c, 'withdrawn');
		this.#queue = this.#queue.filter((q) => q !== c);
		if (c === this.#current) {
			this.#current = undefined;
			this.playing = undefined;
		}
		this.#pump();
	}

	#frame(id: string, seq: number, data: Uint8Array) {
		const c = this.#calls.get(id);
		if (!c || c.done) return;
		if (c.stream) c.stream.decode(data, seq);
		else c.packets.push({ seq, data: data.slice() });
	}

	#alert(c: Call) {
		const ctx = this.#ctx;
		if (!ctx || !this.#alertOut || this.paused || !this.#opts.alerts?.()) return;
		if (playAlert(ctx, this.#alertOut, c.info.talkgroup.alert)) announce('radioarchive:alert', { call_id: c.info.callId, alert: c.info.talkgroup.alert });
	}

	#count() {
		this.queued = this.#queue.length;
		const held = this.hold?.key;
		this.heldBack = held ? this.#queue.filter((c) => !heldThrough(c.info, held)).length : 0;
	}

	/** Starts the next call if nothing is playing. */
	#pump() {
		this.#count();
		if (this.#current || this.phase !== 'listening' || this.paused) return;
		const i = nextIndex(
			this.#queue.map(ruled),
			(c) => this.#priority(c),
			this.hold?.key
		);
		if (i < 0) {
			this.playing = undefined;
			return;
		}
		const [c] = this.#queue.splice(i, 1);
		this.#count();
		this.#current = c;
		if (c.ended && !c.recording) {
			// Ended by its turn: its recording plays (R43).
			c.recording = recordingURL(c.info.callId);
		}
		if (c.recording) {
			c.info.source = 'recording';
			this.playing = { ...c.info };
			void this.#playRecording(c);
			return;
		}
		this.playing = { ...c.info };
		this.#open(c);
		for (const p of c.packets) c.stream!.decode(p.data, p.seq);
		c.packets = [];
		this.#tick();
	}

	#open(c: Call) {
		c.stream = this.#dec!.open((pcm) => this.#play(c, pcm));
	}

	async #playRecording(c: Call) {
		let packets: Uint8Array[] = [];
		// A call that has just ended may not have its recording in yet: it
		// comes on its own connection. One that's still unavailable after a
		// few seconds (withdrawn, R31) is skipped.
		for (let i = 0; i < RECORDING_TRIES && c === this.#current; i++) {
			try {
				const r = await fetch(c.recording!);
				if (r.ok) {
					packets = readOggOpus(await r.arrayBuffer()).packets;
					break;
				}
				if (r.status !== 404) break;
			} catch {
				// The network: tried again.
			}
			await new Promise((res) => setTimeout(res, 1000));
		}
		if (c !== this.#current) return;
		this.#open(c);
		if (packets.length) {
			c.packets = [];
			packets.forEach((p, i) => c.stream!.decode(p, i));
		} else if (c.packets.length) {
			// No recording (a call the recorder lost): what was heard of it live.
			c.info.source = 'live';
			this.playing = { ...c.info };
			for (const p of c.packets) c.stream!.decode(p.data, p.seq);
			c.packets = [];
		}
		this.#finish(c);
	}

	/** Schedules one decoded frame after the call's last. */
	#play(c: Call, pcm: Pcm) {
		const ctx = this.#ctx;
		if (!ctx || c !== this.#current || c.done || pcm.samples.length === 0) return;
		if (!c.firstFrame) {
			c.firstFrame = true;
			announce('radioarchive:first-frame', { call_id: c.info.callId, unit: c.info.unit?.id ?? null, path: this.path, source: c.info.source, at: Date.now() });
		}
		c.frames++;
		const buf = ctx.createBuffer(1, pcm.samples.length, pcm.sampleRate);
		buf.copyToChannel(pcm.samples as Float32Array<ArrayBuffer>, 0);
		const src = ctx.createBufferSource();
		src.buffer = buf;
		src.connect(this.#out!);
		// Behind (a gap in the feed, or the first frame): start again a little ahead.
		const at = c.endsAt < ctx.currentTime + 0.005 ? ctx.currentTime + LEAD : c.endsAt;
		src.start(at);
		c.startsAt ??= at;
		c.endsAt = at + buf.duration;
		c.sources.push(src);
		src.onended = () => {
			const i = c.sources.indexOf(src);
			if (i >= 0) c.sources.splice(i, 1);
		};
	}

	/** The call has ended: once its audio has played, the next one starts. */
	async #finish(c: Call) {
		if (c.finishing) return;
		c.finishing = true;
		await c.stream?.flush();
		const wait = () => {
			if (c !== this.#current || !this.#ctx) return;
			const left = c.endsAt - this.#ctx.currentTime;
			if (left > 0) {
				setTimeout(wait, Math.min(left * 1000 + 30, 1000));
				return;
			}
			this.#measure();
			this.#done(c);
		};
		wait();
	}

	#done(c: Call) {
		c.done = true;
		c.stream?.close();
		if (!c.first) {
			this.#calls.delete(c.info.callId);
			this.#forget(c.info.callId);
		}
		announce('radioarchive:played', { call_id: c.info.callId, path: this.path, source: c.info.source, frames: c.frames, peak: c.peak });
		if (c.frames > 0) this.last = { ...c.info };
		this.#current = undefined;
		this.playing = undefined;
		this.#pump();
	}

	/** A call stops, or leaves the queue, unplayed: skipped, cleared or withdrawn. */
	#leave(c: Call, how: 'skipped' | 'cleared' | 'withdrawn') {
		this.#silence(c);
		if (!c.first) {
			this.#calls.delete(c.info.callId);
			this.#forget(c.info.callId);
		}
		announce('radioarchive:ended', { call_id: c.info.callId, how, at: Date.now() });
	}

	/** Remembers that a call has left, so what comes of it later is ignored. */
	#forget(id: string) {
		const now = Date.now();
		this.#gone.set(id, now);
		if (this.#gone.size % 64 === 0) for (const [k, at] of this.#gone) if (now - at > FORGET_AFTER_MS) this.#gone.delete(k);
	}

	#stopSources(c: Call) {
		for (const s of c.sources) {
			try {
				s.stop();
			} catch {
				// Not started yet, or ended.
			}
			s.disconnect();
		}
		c.sources = [];
	}

	#silence(c: Call) {
		c.done = true;
		this.#stopSources(c);
		c.packets = [];
		c.stream?.close();
	}

	/** Where the call playing is (R45): how far behind the live edge, and its duration so far. */
	#tick() {
		const c = this.#current;
		const ctx = this.#ctx;
		if (!c || !ctx) {
			if (this.position.behind || this.position.elapsed) this.position = { behind: 0, elapsed: 0 };
			return;
		}
		const now = ctx.currentTime;
		const played = c.startsAt === undefined ? 0 : Math.max(0, now - c.startsAt);
		let behind: number;
		let elapsed: number;
		if (c.info.source === 'live') {
			// What's scheduled and not yet heard is how far behind the radio it is.
			behind = Math.max(0, c.endsAt - now - LEAD);
			const since = (Date.now() - Date.parse(c.info.startedAt)) / 1000;
			elapsed = c.ended && c.info.durationMs !== undefined ? c.info.durationMs / 1000 : Math.max(played, since);
		} else {
			// A recording: as far behind as the moment it plays is from now.
			behind = Math.max(0, (Date.now() - Date.parse(c.info.startedAt)) / 1000 - played);
			elapsed = played;
		}
		const next = { behind: Math.round(behind), elapsed: Math.floor(elapsed) };
		if (next.behind !== this.position.behind || next.elapsed !== this.position.elapsed) this.position = next;
	}

	#measure() {
		const c = this.#current;
		if (!c || !this.#meter || !this.#meterBuf) return;
		this.#meter.getFloatTimeDomainData(this.#meterBuf);
		let peak = c.peak;
		for (const v of this.#meterBuf) if (Math.abs(v) > peak) peak = Math.abs(v);
		c.peak = peak;
	}
}
