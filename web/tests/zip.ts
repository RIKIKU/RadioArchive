/// <reference types="node" />
// Reading a zip the API made (ADR 0015: stored entries, no Zip64), for the
// basket's tests: its entries' names in order, and an entry's bytes.

/** Each entry in the central directory, in order: its name and its bytes. */
export function unzip(zip: Buffer): { name: string; data: Buffer }[] {
	let end = zip.length - 22;
	while (end >= 0 && zip.readUInt32LE(end) !== 0x06054b50) end--;
	if (end < 0) throw new Error('not a zip: no end of central directory');
	const count = zip.readUInt16LE(end + 10);
	let at = zip.readUInt32LE(end + 16);
	const out: { name: string; data: Buffer }[] = [];
	for (let i = 0; i < count; i++) {
		if (zip.readUInt32LE(at) !== 0x02014b50) throw new Error(`not a zip: entry ${i} has no central header`);
		const size = zip.readUInt32LE(at + 20);
		const n = zip.readUInt16LE(at + 28);
		const m = zip.readUInt16LE(at + 30);
		const k = zip.readUInt16LE(at + 32);
		const local = zip.readUInt32LE(at + 42);
		const name = zip.toString('utf8', at + 46, at + 46 + n);
		const start = local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
		out.push({ name, data: zip.subarray(start, start + size) });
		at += 46 + n + m + k;
	}
	return out;
}

export const zipNames = (zip: Buffer): string[] => unzip(zip).map((e) => e.name);

export function zipEntry(zip: Buffer, name: string): Buffer {
	const e = unzip(zip).find((x) => x.name === name);
	if (!e) throw new Error(`no ${name} in the zip`);
	return e.data;
}
