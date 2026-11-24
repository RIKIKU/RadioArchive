// Opus decoding in the browser (SPEC.md R39, ADR 0011): WebCodecs'
// AudioDecoder where the browser has it with Opus support, and otherwise a
// WASM build of libopus (opus-decoder), bundled into the app and served
// from the binary like the rest of it. The WASM is loaded only when the
// browser needs it.
//
// Both decode the same thing: R4's packets, mono, 20 ms each, from the live
// feed or a recording. Both hand back mono float samples, at 48 kHz.

/** Which decoder ran: named by the browser tests (A7). */
export type DecodePath = 'webcodecs' | 'wasm';

/** Decoded audio: mono samples at sampleRate. */
export interface Pcm {
	samples: Float32Array;
	sampleRate: number;
}

/** One call's decoding: packets in, PCM out, in order. */
export interface OpusStream {
	/** Decodes one packet. seq is its frame number, for its timestamp. */
	decode(packet: Uint8Array, seq: number): void;
	/** Resolves once every packet given has been decoded. */
	flush(): Promise<void>;
	close(): void;
}

export interface Decoding {
	path: DecodePath;
	/** Opens a stream for one call; onPcm gets its audio. */
	open(onPcm: (p: Pcm) => void): OpusStream;
}

const RATE = 48_000;
const FRAME_US = 20_000;
const config: AudioDecoderConfig = { codec: 'opus', sampleRate: RATE, numberOfChannels: 1 };

/** Whether this browser's WebCodecs decodes Opus. */
async function webCodecsOpus(): Promise<boolean> {
	if (typeof globalThis.AudioDecoder !== 'function' || typeof globalThis.EncodedAudioChunk !== 'function') return false;
	try {
		return (await AudioDecoder.isConfigSupported(config)).supported === true;
	} catch {
		return false;
	}
}

/** The first channel of d as float samples, whatever format the browser gave. */
function samplesOf(d: AudioData): Float32Array {
	const n = d.numberOfFrames;
	const out = new Float32Array(n);
	try {
		d.copyTo(out, { planeIndex: 0, format: 'f32-planar' });
		return out;
	} catch {
		// A browser that can't convert: read its own format.
	}
	const fmt = d.format;
	const ch = d.numberOfChannels;
	if (fmt === 'f32' || fmt === 'f32-planar') {
		const all = new Float32Array(fmt === 'f32' ? n * ch : n);
		d.copyTo(all, { planeIndex: 0 });
		if (fmt === 'f32-planar' || ch === 1) return all.subarray(0, n);
		for (let i = 0; i < n; i++) out[i] = all[i * ch];
		return out;
	}
	if (fmt === 's16' || fmt === 's16-planar') {
		const all = new Int16Array(fmt === 's16' ? n * ch : n);
		d.copyTo(all, { planeIndex: 0 });
		const step = fmt === 's16' ? ch : 1;
		for (let i = 0; i < n; i++) out[i] = all[i * step] / 32768;
		return out;
	}
	return out;
}

const webCodecs: Decoding = {
	path: 'webcodecs',
	open(onPcm) {
		let broken = false;
		const dec = new AudioDecoder({
			output: (d) => {
				try {
					onPcm({ samples: samplesOf(d), sampleRate: d.sampleRate });
				} finally {
					d.close();
				}
			},
			error: () => {
				broken = true;
			}
		});
		dec.configure(config);
		return {
			decode(packet, seq) {
				if (broken || dec.state !== 'configured') return;
				try {
					dec.decode(new EncodedAudioChunk({ type: 'key', timestamp: seq * FRAME_US, duration: FRAME_US, data: packet }));
				} catch {
					// A packet the decoder refuses is skipped.
				}
			},
			async flush() {
				if (broken || dec.state !== 'configured') return;
				try {
					await dec.flush();
				} catch {
					// Closed or broken meanwhile: nothing more comes.
				}
			},
			close() {
				if (dec.state !== 'closed') dec.close();
			}
		};
	}
};

type WasmDecoder = {
	ready: Promise<void>;
	decodeFrame(p: Uint8Array): { channelData: Float32Array[]; samplesDecoded: number; sampleRate: number };
	free(): void;
};
type WasmDecoderClass = new (o: object) => WasmDecoder;

/** opus-decoder's options for R4's mono stream. */
const wasmOptions = { sampleRate: RATE, channels: 1, streamCount: 1, coupledStreamCount: 0, channelMappingTable: [0], preSkip: 0 };

function wasm(OpusDecoder: WasmDecoderClass): Decoding {
	return {
		path: 'wasm',
		open(onPcm) {
			const dec = new OpusDecoder(wasmOptions);
			// Packets that arrive while the decoder starts wait for it.
			let waiting: Uint8Array[] | undefined = [];
			let closed = false;
			const run = (p: Uint8Array) => {
				try {
					const r = dec.decodeFrame(p);
					if (r.samplesDecoded > 0) onPcm({ samples: r.channelData[0].slice(0, r.samplesDecoded), sampleRate: r.sampleRate });
				} catch {
					// A packet the decoder refuses is skipped.
				}
			};
			const ready = dec.ready.then(() => {
				if (closed) return;
				for (const p of waiting ?? []) run(p);
				waiting = undefined;
			});
			return {
				decode(packet) {
					if (closed) return;
					if (waiting) waiting.push(packet.slice());
					else run(packet);
				},
				flush: () => ready,
				close() {
					if (closed) return;
					closed = true;
					ready.then(() => dec.free());
				}
			};
		}
	};
}

let chosen: Promise<Decoding> | undefined;

/**
 * The decoding this browser uses: WebCodecs if it decodes Opus, else the
 * WASM decoder, loaded and started here so a call's first frame doesn't wait
 * for it.
 */
export function decoding(): Promise<Decoding> {
	chosen ??= (async () => {
		if (await webCodecsOpus()) return webCodecs;
		const { OpusDecoder } = await import('opus-decoder');
		const d = wasm(OpusDecoder as unknown as WasmDecoderClass);
		// Compile the module once, now.
		const warm = new (OpusDecoder as unknown as WasmDecoderClass)(wasmOptions);
		await warm.ready;
		warm.free();
		return d;
	})();
	chosen.catch(() => (chosen = undefined));
	return chosen;
}
