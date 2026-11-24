// Replaying a call from the archive (SPEC.md R46; ADR 0016): pressing a
// call in recent calls plays its recording here, through an output of its
// own, one call at a time. The player (player.svelte.ts) pauses live
// listening while a call is replayed, so live calls keep queuing; this
// never touches the queue.
//
// Its own AudioContext, because pausing live listening suspends the live
// output's (ADR 0012): the replay has to go on while that clock is stopped.
// It's made from the press, as browsers let audio start only from a
// gesture.
//
// Several calls play one after another on the same output: a
// conversation's Play all, and play from here (ADR 0023), whose calls are
// fetched a page at a time as it goes. When replaying is over (played out,
// failed or stopped) it calls onend, and the player resumes live listening
// if it paused it for the replay (ADR 0016 as amended, ADR 0023).
//
// It announces on the window, as the player does, with replay: true:
//
//   radioarchive:first-frame  {call_id, unit, path, source: 'recording', replay, at}
//   radioarchive:played       {call_id, path, source: 'recording', replay, frames, peak}
//   radioarchive:ended        {call_id, how: 'stopped', replay, at}   stopped before its end

import { decoding, type OpusStream, type Pcm } from './decoder.ts';
import { readOggOpus } from './ogg.ts';
import type { CallInfo } from './player.svelte.ts';

/** A call to replay: what the dock shows of it, its recording, and when it started, as shown (when). */
export interface ReplayItem {
	info: CallInfo;
	url: string;
	when?: string;
}

/** Play from here's next page of calls, and whether there are more after it. */
export type MoreCalls = () => Promise<{ items: ReplayItem[]; more: boolean }>;

/** How far ahead of now the first frame is scheduled. */
const LEAD = 0.1;

function announce(name: string, detail: object) {
	window.dispatchEvent(new CustomEvent(name, { detail }));
}

export class Replay {
	/** The call being replayed. */
	call: CallInfo | undefined = $state();
	/** How much of it has played, in seconds. */
	elapsed = $state(0);
	/** Why the last call couldn't be replayed, if it couldn't. */
	failure = $state('');

	#ctx: AudioContext | undefined;
	#out: GainNode | undefined;
	#meter: AnalyserNode | undefined;
	#meterBuf: Float32Array<ArrayBuffer> | undefined;
	/** Each play is a generation: what an earlier one finishes later is ignored. */
	#gen = 0;
	#stream: OpusStream | undefined;
	#sources: AudioBufferSourceNode[] = [];
	#timer: ReturnType<typeof setInterval> | undefined;
	#startsAt: number | undefined;
	#endsAt = 0;
	#frames = 0;
	#peak = 0;
	#firstFrame = false;

	/**
	 * Calls played one after another: a conversation's Play all (R64; ADR
	 * 0018), or play from here (R46; ADR 0023). Its name, which of its calls
	 * is playing and when that call started (when), how many it has so far,
	 * and whether more are still to be fetched.
	 */
	list: { name: string; at: number; of: number; more: boolean; when?: string } | undefined = $state();
	/**
	 * Called once replaying is over: the last call played to its end, one
	 * couldn't be played, or Stop. Never between the calls of a list, nor
	 * when a new press replaces what was replaying. The player resumes live
	 * listening from it (ADR 0023).
	 */
	onend: (() => void) | undefined;
	#queue: ReplayItem[] = [];
	/** Play from here: fetches the calls after those queued. */
	#more: MoreCalls | undefined;
	#fetching: Promise<void> | undefined;
	#moreFailed = false;
	/** Each press is a run: what an earlier one fetches later is ignored. */
	#press = 0;

	/** Plays a call's recording from its start. Call it from the press. */
	play(info: CallInfo, url: string) {
		this.#begin();
		this.list = undefined;
		this.#start(info, url);
	}

	/**
	 * Plays calls one after another, in their order, from their recordings:
	 * a conversation's Play all. Call it from the press; the rest follow on
	 * the same output. Stop ends them all.
	 */
	playAll(name: string, calls: ReplayItem[]) {
		if (!calls.length) return;
		this.#begin();
		this.#queue = calls.slice(1);
		this.list = { name, at: 1, of: calls.length, more: false, when: calls[0].when };
		this.#start(calls[0].info, calls[0].url);
	}

	/**
	 * Play from here (R46; ADR 0023): plays first, then the calls more
	 * gives, a page at a time, fetched ahead while one plays. Call it from
	 * the press. It ends when more has none left, or on Stop.
	 */
	playRun(name: string, first: ReplayItem, more: MoreCalls) {
		this.#begin();
		this.#more = more;
		this.list = { name, at: 1, of: 1, more: true, when: first.when };
		this.#start(first.info, first.url);
		this.#topUp();
	}

	#begin() {
		this.#press++;
		this.#queue = [];
		this.#more = undefined;
		this.#fetching = undefined;
		this.#moreFailed = false;
	}

	/** Fetches the next page of a run while there's at most one call left queued. */
	#topUp() {
		const more = this.#more;
		if (!more || this.#fetching || this.#queue.length > 1) return;
		const run = this.#press;
		this.#fetching = more()
			.then(
				({ items, more: left }) => {
					if (run !== this.#press) return;
					this.#queue.push(...items);
					if (!left) this.#more = undefined;
					if (this.list) this.list = { ...this.list, of: this.list.of + items.length, more: left };
				},
				() => {
					if (run !== this.#press) return;
					this.#more = undefined;
					this.#moreFailed = true;
					if (this.list) this.list = { ...this.list, more: false };
				}
			)
			.finally(() => {
				if (run === this.#press) this.#fetching = undefined;
			});
	}

	/** After a call of a list has played: the next one, fetching it first if need be; else it's over. */
	async #advance() {
		const run = this.#press;
		let next = this.#queue.shift();
		while (!next && (this.#fetching || this.#more)) {
			this.#topUp();
			await this.#fetching;
			if (run !== this.#press) return;
			next = this.#queue.shift();
		}
		if (next && this.list) {
			this.list = { ...this.list, at: this.list.at + 1, when: next.when };
			this.#start(next.info, next.url);
			this.#topUp();
			return;
		}
		const failed = this.#moreFailed;
		this.#begin();
		this.list = undefined;
		if (failed) this.failure = "Couldn't load the calls after that one.";
		this.onend?.();
	}

	#start(info: CallInfo, url: string) {
		this.#stopCurrent();
		const gen = ++this.#gen;
		if (!this.#ctx) {
			const ctx = new AudioContext({ latencyHint: 'interactive' });
			this.#ctx = ctx;
			this.#out = ctx.createGain();
			this.#meter = ctx.createAnalyser();
			this.#meter.fftSize = 2048;
			this.#meterBuf = new Float32Array(this.#meter.fftSize);
			this.#out.connect(this.#meter);
			this.#meter.connect(ctx.destination);
		}
		// Resumed inside the gesture, before any await.
		const resumed = this.#ctx.resume().catch(() => undefined);
		this.call = { ...info, source: 'recording', delayMinutes: undefined };
		this.elapsed = 0;
		this.failure = '';
		void this.#run(gen, url, resumed);
	}

	/** Stops the call being replayed, if one is, and the calls after it. */
	stop() {
		const replaying = !!this.call || !!this.list;
		this.#begin();
		this.list = undefined;
		this.#stopCurrent();
		if (replaying) this.onend?.();
	}

	#stopCurrent() {
		const c = this.call;
		this.#gen++;
		this.#halt();
		if (c) announce('radioarchive:ended', { call_id: c.callId, how: 'stopped', replay: true, at: Date.now() });
	}

	async #run(gen: number, url: string, resumed: Promise<unknown>) {
		let packets: Uint8Array[];
		let path: string;
		try {
			const [dec, body] = await Promise.all([
				decoding(),
				fetch(url).then((r) => (r.ok ? r.arrayBuffer() : Promise.reject(new Error(String(r.status)))))
			]);
			packets = readOggOpus(body).packets;
			await resumed;
			if (gen !== this.#gen) return;
			path = dec.path;
			this.#frames = 0;
			this.#peak = 0;
			this.#firstFrame = false;
			this.#startsAt = undefined;
			this.#endsAt = 0;
			this.#stream = dec.open((pcm) => this.#schedule(gen, pcm, path));
		} catch (err) {
			if (gen !== this.#gen) return;
			this.#halt();
			this.#begin();
			this.list = undefined;
			this.failure = String(err).includes('404') ? "This call isn't available any more." : "Couldn't play that call. Try again.";
			this.onend?.();
			return;
		}
		this.#timer = setInterval(() => this.#tick(), 50);
		try {
			const stream = this.#stream!;
			packets.forEach((p, i) => stream.decode(p, i));
			await stream.flush();
		} catch {
			// A packet the decoder refused: what decoded plays, and it ends.
		}
		const wait = () => {
			if (gen !== this.#gen || !this.#ctx) return;
			const left = this.#endsAt - this.#ctx.currentTime;
			if (left > 0) {
				setTimeout(wait, Math.min(left * 1000 + 30, 500));
				return;
			}
			this.#tick();
			const c = this.call;
			announce('radioarchive:played', { call_id: c?.callId, path, source: 'recording', replay: true, frames: this.#frames, peak: this.#peak });
			this.#gen++;
			this.#halt();
			// A list: the next call, on the output the press started.
			if (this.list) void this.#advance();
			else this.onend?.();
		};
		wait();
	}

	#schedule(gen: number, pcm: Pcm, path: string) {
		const ctx = this.#ctx;
		if (!ctx || gen !== this.#gen || pcm.samples.length === 0) return;
		if (!this.#firstFrame) {
			this.#firstFrame = true;
			announce('radioarchive:first-frame', { call_id: this.call?.callId, unit: this.call?.unit?.id ?? null, path, source: 'recording', replay: true, at: Date.now() });
		}
		this.#frames++;
		const buf = ctx.createBuffer(1, pcm.samples.length, pcm.sampleRate);
		buf.copyToChannel(pcm.samples as Float32Array<ArrayBuffer>, 0);
		const src = ctx.createBufferSource();
		src.buffer = buf;
		src.connect(this.#out!);
		const at = this.#endsAt < ctx.currentTime + 0.005 ? ctx.currentTime + LEAD : this.#endsAt;
		src.start(at);
		this.#startsAt ??= at;
		this.#endsAt = at + buf.duration;
		this.#sources.push(src);
		src.onended = () => {
			const i = this.#sources.indexOf(src);
			if (i >= 0) this.#sources.splice(i, 1);
		};
	}

	/** Where it is, and the loudest sample heard so far. */
	#tick() {
		const ctx = this.#ctx;
		if (!ctx || !this.call) return;
		if (this.#meter && this.#meterBuf) {
			this.#meter.getFloatTimeDomainData(this.#meterBuf);
			for (const v of this.#meterBuf) if (Math.abs(v) > this.#peak) this.#peak = Math.abs(v);
		}
		const played = this.#startsAt === undefined ? 0 : Math.max(0, Math.min(ctx.currentTime, this.#endsAt) - this.#startsAt);
		const s = Math.floor(played);
		if (s !== this.elapsed) this.elapsed = s;
	}

	#halt() {
		clearInterval(this.#timer);
		this.#timer = undefined;
		for (const s of this.#sources) {
			try {
				s.stop();
			} catch {
				// Not started yet, or ended.
			}
			s.disconnect();
		}
		this.#sources = [];
		this.#stream?.close();
		this.#stream = undefined;
		this.call = undefined;
		this.elapsed = 0;
	}
}
