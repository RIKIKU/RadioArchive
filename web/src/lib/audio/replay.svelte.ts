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
// It announces on the window, as the player does, with replay: true:
//
//   radioarchive:first-frame  {call_id, unit, path, source: 'recording', replay, at}
//   radioarchive:played       {call_id, path, source: 'recording', replay, frames, peak}
//   radioarchive:ended        {call_id, how: 'stopped', replay, at}   stopped before its end

import { decoding, type OpusStream, type Pcm } from './decoder.ts';
import { readOggOpus } from './ogg.ts';
import type { CallInfo } from './player.svelte.ts';

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

	/** Plays a call's recording from its start. Call it from the press. */
	play(info: CallInfo, url: string) {
		this.stop();
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

	/** Stops the call being replayed, if one is. */
	stop() {
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
			this.failure = String(err).includes('404') ? "This call isn't available any more." : "Couldn't play that call. Try again.";
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
