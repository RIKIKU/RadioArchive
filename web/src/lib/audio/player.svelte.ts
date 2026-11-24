// The player (SPEC.md P2, R38, R39, R43, R44; ADR 0011): the live feed's
// calls, decoded (decoder.ts) and played into Web Audio, one at a time, and
// a finished call played from its recording through the same output.
//
// This is the first slice of the player. Calls that start while one plays
// wait their turn in the order they started; a call that has ended by its
// turn with nothing heard of it plays from its recording. Priority, skip,
// pause, hold and the dock are #60's.
//
// For the browser tests (A6, A7), and anything else that wants to know, the
// player announces on the window:
//
//   radioarchive:first-frame  {call_id, unit, path, source, at}  a call's first decoded frame (at: Unix ms)
//   radioarchive:played       {call_id, path, source, frames, peak}  a call has finished playing

import { attachFeed } from '#lib/live/listening-set.ts';
import { LiveFeed, type CallEvent, type EventTalkgroup } from '#lib/live/feed.ts';
import { decoding, type Decoding, type DecodePath, type OpusStream, type Pcm } from './decoder.ts';
import { readOggOpus } from './ogg.ts';

export type Source = 'live' | 'recording';

/** What the page shows of the call playing, or last heard. */
export interface CallInfo {
	callId: string;
	talkgroup: EventTalkgroup;
	unit: { id: number; name: string } | null;
	startedAt: string;
	source: Source;
	delayMinutes?: number;
}

interface Call {
	info: CallInfo;
	ended: boolean;
	recording?: string;
	/** Frames heard before its turn. */
	packets: { seq: number; data: Uint8Array }[];
	stream?: OpusStream;
	firstFrame: boolean;
	frames: number;
	peak: number;
	/** When its scheduled audio ends, in the output's time. */
	endsAt: number;
	sources: AudioBufferSourceNode[];
	finishing?: boolean;
	done?: boolean;
}

/** How far ahead of now the first frame of a run is scheduled: room for the network's jitter. */
const LEAD = 0.15;
/** How many times a recording is asked for, a second apart, before it's skipped. */
const RECORDING_TRIES = 5;

const recordingURL = (id: string) => `/api/v1/calls/${encodeURIComponent(id)}/recording`;

function announce(name: string, detail: object) {
	window.dispatchEvent(new CustomEvent(name, { detail }));
}

export class Player {
	/** stopped, starting (the gesture's been made), or listening. */
	phase: 'stopped' | 'starting' | 'listening' = $state('stopped');
	/** Whether the live feed is connected now. */
	connected = $state(false);
	/** Which decoder plays (R39). */
	path: DecodePath | undefined = $state();
	/** The call playing. */
	playing: CallInfo | undefined = $state();
	/** Calls waiting their turn. */
	waiting = $state(0);
	/** The last call heard to its end, to play again from its recording. */
	last: CallInfo | undefined = $state();
	/** Why listening couldn't start, if it couldn't. */
	failure = $state('');

	#ctx: AudioContext | undefined;
	#out: GainNode | undefined;
	#meter: AnalyserNode | undefined;
	#meterBuf: Float32Array<ArrayBuffer> | undefined;
	#meterTimer: ReturnType<typeof setInterval> | undefined;
	#dec: Decoding | undefined;
	#feed: LiveFeed | undefined;
	#calls = new Map<string, Call>();
	#queue: Call[] = [];
	#current: Call | undefined;

	/**
	 * Starts listening: call it from the Start listening button's click
	 * (R44), since the browser lets audio start only from a gesture.
	 */
	async start() {
		if (this.phase !== 'stopped') return;
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
		const feed = new LiveFeed({
			event: (e) => this.#event(e),
			withdrawn: (id) => this.#withdraw(id),
			frame: (id, seq, p) => this.#frame(id, seq, p),
			connected: (on) => (this.connected = on)
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
		for (const c of this.#calls.values()) this.#silence(c);
		this.#calls.clear();
		this.#queue = [];
		this.#current = undefined;
		this.#ctx?.close().catch(() => undefined);
		this.#ctx = undefined;
		this.playing = undefined;
		this.waiting = 0;
		this.connected = false;
		this.phase = 'stopped';
	}

	/** Plays a finished call again, from its recording (R20), after the one playing. */
	playAgain(info: CallInfo) {
		if (this.phase !== 'listening') return;
		const c = this.#newCall({ ...info, source: 'recording', delayMinutes: undefined }, true, recordingURL(info.callId));
		this.#queue.unshift(c);
		this.#pump();
	}

	#newCall(info: CallInfo, ended: boolean, recording?: string): Call {
		return { info, ended, recording, packets: [], firstFrame: false, frames: 0, peak: 0, endsAt: 0, sources: [] };
	}

	#infoOf(e: CallEvent, source: Source): CallInfo {
		return { callId: e.call_id, talkgroup: e.talkgroup, unit: e.unit, startedAt: e.started_at, source, delayMinutes: e.delay_minutes };
	}

	#event(e: CallEvent) {
		let c = this.#calls.get(e.call_id);
		switch (e.type) {
			case 'call_start':
				if (c) return;
				c = this.#newCall(this.#infoOf(e, 'live'), false);
				this.#calls.set(e.call_id, c);
				this.#queue.push(c);
				this.#pump();
				return;
			case 'transmission':
				if (!c) return;
				c.info.unit = e.unit;
				if (c === this.#current) this.playing = { ...c.info };
				return;
			case 'call_end':
				if (!c) {
					// Missed while away (a resume, R37): played from its recording.
					c = this.#newCall(this.#infoOf(e, 'recording'), true, recordingURL(e.call_id));
					this.#calls.set(e.call_id, c);
					this.#queue.push(c);
					this.#pump();
					return;
				}
				c.ended = true;
				if (c.packets.length === 0 && !c.stream && c !== this.#current) {
					// Nothing heard of it live: its recording plays (R43).
					c.recording = recordingURL(e.call_id);
					c.info.source = 'recording';
				}
				if (c === this.#current) this.#finish(c);
				return;
			case 'call_delayed':
				// A delayed talkgroup's call (R23): queued, from its recording.
				if (c) return;
				c = this.#newCall(this.#infoOf(e, 'recording'), true, e.recording?.url ?? recordingURL(e.call_id));
				this.#calls.set(e.call_id, c);
				this.#queue.push(c);
				this.#pump();
				return;
		}
	}

	#withdraw(id: string) {
		const c = this.#calls.get(id);
		if (!c) return;
		this.#calls.delete(id);
		this.#queue = this.#queue.filter((q) => q !== c);
		this.waiting = this.#queue.length;
		if (c === this.#current) {
			this.#silence(c);
			this.#current = undefined;
			this.playing = undefined;
			this.#pump();
		}
	}

	#frame(id: string, seq: number, data: Uint8Array) {
		const c = this.#calls.get(id);
		if (!c || c.done) return;
		if (c.stream) c.stream.decode(data, seq);
		else c.packets.push({ seq, data: data.slice() });
	}

	/** Starts the next call if nothing is playing. */
	#pump() {
		this.waiting = this.#queue.length;
		if (this.#current || this.phase !== 'listening') return;
		const c = this.#queue.shift();
		this.waiting = this.#queue.length;
		if (!c) {
			this.playing = undefined;
			return;
		}
		this.#current = c;
		this.playing = { ...c.info };
		if (c.recording && c.packets.length === 0) {
			void this.#playRecording(c);
			return;
		}
		this.#open(c);
		for (const p of c.packets) c.stream!.decode(p.data, p.seq);
		c.packets = [];
		if (c.ended) this.#finish(c);
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
		packets.forEach((p, i) => c.stream!.decode(p, i));
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
		this.#calls.delete(c.info.callId);
		announce('radioarchive:played', { call_id: c.info.callId, path: this.path, source: c.info.source, frames: c.frames, peak: c.peak });
		if (c.frames > 0) this.last = { ...c.info };
		this.#current = undefined;
		this.playing = undefined;
		this.#pump();
	}

	#silence(c: Call) {
		c.done = true;
		for (const s of c.sources) {
			try {
				s.stop();
			} catch {
				// Not started yet, or ended.
			}
			s.disconnect();
		}
		c.sources = [];
		c.stream?.close();
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
