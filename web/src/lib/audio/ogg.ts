// Reads a recording (SPEC.md R4, R20: Ogg/Opus) into its Opus packets, so
// it plays through the same decoder and output as the live feed (R39).
// RFC 3533 pages, RFC 7845 for Opus: the first packet is OpusHead, the
// second OpusTags, and every one after is audio.

export interface OggOpus {
	channels: number;
	preSkip: number;
	packets: Uint8Array[];
}

const ascii = (b: Uint8Array, at: number, n: number) => String.fromCharCode(...b.subarray(at, at + n));

/** The packets of the first logical stream in an Ogg file. */
export function oggPackets(buf: ArrayBuffer): Uint8Array[] {
	const b = new Uint8Array(buf);
	const out: Uint8Array[] = [];
	let serial: number | undefined;
	let partial: Uint8Array[] = [];
	const view = new DataView(buf);
	let at = 0;
	while (at + 27 <= b.length) {
		if (ascii(b, at, 4) !== 'OggS') throw new Error('not an Ogg page');
		const pageSerial = view.getUint32(at + 14, true);
		const segments = b[at + 26];
		const table = at + 27;
		let body = table + segments;
		if (body > b.length) break;
		const mine = serial === undefined || serial === pageSerial;
		serial ??= pageSerial;
		for (let i = 0; i < segments; i++) {
			const len = b[table + i];
			if (body + len > b.length) return out;
			if (mine) partial.push(b.subarray(body, body + len));
			body += len;
			if (mine && len < 255) {
				const size = partial.reduce((n, p) => n + p.length, 0);
				const packet = new Uint8Array(size);
				let o = 0;
				for (const p of partial) {
					packet.set(p, o);
					o += p.length;
				}
				out.push(packet);
				partial = [];
			}
		}
		at = body;
	}
	return out;
}

/** An Ogg/Opus recording's header and audio packets. */
export function readOggOpus(buf: ArrayBuffer): OggOpus {
	const [head, tags, ...packets] = oggPackets(buf);
	if (!head || head.length < 19 || ascii(head, 0, 8) !== 'OpusHead') throw new Error('not Ogg/Opus');
	if (!tags || ascii(tags, 0, 8) !== 'OpusTags') throw new Error('Ogg/Opus without OpusTags');
	const v = new DataView(head.buffer, head.byteOffset, head.byteLength);
	return { channels: head[9], preSkip: v.getUint16(10, true), packets };
}
