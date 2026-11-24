// The download basket (SPEC.md P1, P4, P5, R64, R65, R67, R72, R88; ADR
// 0015, ADR 0018): what it keeps, how it changes, its countdowns, and the
// zip URLs its downloads take. Nothing here touches the page, storage or
// the network, so it can be tested on its own (tests/basket-rules.spec.ts).
//
// The basket keeps call IDs, when each was added, and the listener's own
// conversations (a name, an optional note, calls in order), never anything
// of a call's talkgroup: what it shows of a call comes from the call
// details each time, so a call hidden since it was added shows nothing of
// its talkgroup (P1). It's kept in the browser (localStorage), and in the
// listener's profile when signed in (ADR 0009), in this same form.

/** A call in the basket, or in a conversation, and when it was put there (Unix ms). */
export interface BasketCall {
	id: string;
	added: number;
}

/** A named group of the basket's calls, in order, with an optional note (R64). */
export interface Conversation {
	/** The basket's own name for it, never shown. */
	id: string;
	name: string;
	note?: string;
	calls: BasketCall[];
	/** When it was last downloaded (Unix ms). */
	last?: number;
}

export interface Basket {
	calls: BasketCall[];
	conversations: Conversation[];
	/** When the whole basket was last downloaded (Unix ms). */
	last?: number;
}

/** How large a basket the profile holds (ADR 0018; internal/account/profile.go). */
export const MAX_CALLS = 1000;
export const MAX_CONVERSATIONS = 50;
export const MAX_PLACES = 2000;
/** A conversation's name and note, in bytes of UTF-8, as a zip takes them (ADR 0015). */
export const MAX_NAME = 200;
export const MAX_NOTE = 2000;

export const emptyBasket = (): Basket => ({ calls: [], conversations: [] });

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const CONV_ID = /^[A-Za-z0-9_-]{1,32}$/;

/** Bytes of UTF-8. */
export const bytesOf = (s: string): number => new TextEncoder().encode(s).length;

const time = (v: unknown): number | undefined => (typeof v === 'number' && Number.isSafeInteger(v) && v >= 0 ? v : undefined);

/**
 * A basket as kept (localStorage, or the profile), read forgivingly:
 * anything that isn't one is left out, so a bad entry never loses the rest.
 */
export function parseBasket(raw: unknown): Basket {
	const b = emptyBasket();
	if (!raw || typeof raw !== 'object') return b;
	const r = raw as Record<string, unknown>;
	const seen = new Set<string>();
	for (const c of Array.isArray(r.calls) ? r.calls : []) {
		const id = typeof c?.id === 'string' ? c.id.toLowerCase() : '';
		const added = time(c?.added);
		if (!UUID.test(id) || added === undefined || seen.has(id) || b.calls.length >= MAX_CALLS) continue;
		seen.add(id);
		b.calls.push({ id, added });
	}
	const ids = new Set<string>();
	let places = 0;
	for (const v of Array.isArray(r.conversations) ? r.conversations : []) {
		if (!v || typeof v !== 'object' || b.conversations.length >= MAX_CONVERSATIONS) continue;
		const id = typeof v.id === 'string' && CONV_ID.test(v.id) && !ids.has(v.id) ? v.id : '';
		const name = typeof v.name === 'string' ? v.name : '';
		const note = typeof v.note === 'string' ? v.note : '';
		if (!id || !name.trim() || bytesOf(name) > MAX_NAME || bytesOf(note) > MAX_NOTE) continue;
		ids.add(id);
		const conv: Conversation = { id, name, calls: [] };
		if (note) conv.note = note;
		const last = time(v.last);
		if (last !== undefined) conv.last = last;
		const inConv = new Set<string>();
		for (const c of Array.isArray(v.calls) ? v.calls : []) {
			const cid = typeof c?.id === 'string' ? c.id.toLowerCase() : '';
			const added = time(c?.added);
			if (!seen.has(cid) || inConv.has(cid) || added === undefined || places >= MAX_PLACES) continue;
			inConv.add(cid);
			places++;
			conv.calls.push({ id: cid, added });
		}
		b.conversations.push(conv);
	}
	const last = time(r.last);
	if (last !== undefined) b.last = last;
	return b;
}

/** Whether a basket holds nothing at all. */
export const isEmpty = (b: Basket): boolean => b.calls.length === 0 && b.conversations.length === 0;

/** Whether two baskets hold the same, in the same order. */
export const sameBasket = (a: Basket, b: Basket): boolean => JSON.stringify(parseBasket(a)) === JSON.stringify(parseBasket(b));

export const has = (b: Basket, id: string): boolean => b.calls.some((c) => c.id === id.toLowerCase());

/** Whether another call fits. */
export const roomFor = (b: Basket): boolean => b.calls.length < MAX_CALLS;

/** The basket with a call added at its end (a call already in it stays where it is). */
export function withCall(b: Basket, id: string, now: number): Basket {
	id = id.toLowerCase();
	if (!UUID.test(id) || has(b, id) || !roomFor(b)) return b;
	return { ...b, calls: [...b.calls, { id, added: now }] };
}

/** The basket without a call, in every conversation too. */
export function withoutCall(b: Basket, id: string): Basket {
	id = id.toLowerCase();
	return {
		...b,
		calls: b.calls.filter((c) => c.id !== id),
		conversations: b.conversations.map((v) => ({ ...v, calls: v.calls.filter((c) => c.id !== id) }))
	};
}

/** What's wrong with a conversation's name and note, field by field (ArchiveUx section 5). */
export function conversationProblems(name: string, note: string): { name?: string; note?: string } {
	const out: { name?: string; note?: string } = {};
	if (!name.trim()) out.name = 'Give the conversation a name.';
	else if (bytesOf(name.trim()) > MAX_NAME) out.name = `Use a shorter name: at most ${MAX_NAME} characters.`;
	if (bytesOf(note.trim()) > MAX_NOTE) out.note = `Use a shorter note: at most ${MAX_NOTE} characters.`;
	return out;
}

/** Whether another conversation fits. */
export const roomForConversation = (b: Basket): boolean => b.conversations.length < MAX_CONVERSATIONS;

const places = (b: Basket) => b.conversations.reduce((n, v) => n + v.calls.length, 0);

/** The basket with a new, empty conversation at its end. */
export function withConversation(b: Basket, id: string, name: string, note: string): Basket {
	if (!CONV_ID.test(id) || b.conversations.some((v) => v.id === id) || !roomForConversation(b)) return b;
	if (Object.keys(conversationProblems(name, note)).length) return b;
	const v: Conversation = { id, name: name.trim(), calls: [] };
	if (note.trim()) v.note = note.trim();
	return { ...b, conversations: [...b.conversations, v] };
}

/** The basket with a conversation's name and note changed. */
export function withConversationEdited(b: Basket, id: string, name: string, note: string): Basket {
	if (Object.keys(conversationProblems(name, note)).length) return b;
	return {
		...b,
		conversations: b.conversations.map((v) => {
			if (v.id !== id) return v;
			const next: Conversation = { ...v, name: name.trim() };
			if (note.trim()) next.note = note.trim();
			else delete next.note;
			return next;
		})
	};
}

/** The basket without a conversation. Its calls stay in the basket. */
export const withoutConversation = (b: Basket, id: string): Basket => ({ ...b, conversations: b.conversations.filter((v) => v.id !== id) });

/**
 * The basket with a call in a conversation (and in the basket, if it
 * wasn't), placed by its start among the conversation's calls when the
 * starts are known (startOf), so the conversation plays in the order the
 * calls happened; else at its end.
 */
export function withInConversation(b: Basket, convId: string, id: string, now: number, startOf: (id: string) => string | undefined = () => undefined): Basket {
	id = id.toLowerCase();
	const v = b.conversations.find((c) => c.id === convId);
	if (!v || v.calls.some((c) => c.id === id) || places(b) >= MAX_PLACES) return b;
	const next = has(b, id) ? b : withCall(b, id, now);
	if (!has(next, id)) return b;
	const start = startOf(id);
	const calls = [...v.calls];
	let at = calls.length;
	if (start) {
		const later = calls.findIndex((c) => {
			const s = startOf(c.id);
			return s !== undefined && s > start;
		});
		if (later >= 0) at = later;
	}
	calls.splice(at, 0, { id, added: now });
	return { ...next, conversations: next.conversations.map((c) => (c.id === convId ? { ...c, calls } : c)) };
}

/** The basket with a call taken out of a conversation. It stays in the basket. */
export const withoutInConversation = (b: Basket, convId: string, id: string): Basket => ({
	...b,
	conversations: b.conversations.map((v) => (v.id === convId ? { ...v, calls: v.calls.filter((c) => c.id !== id) } : v))
});

/** The calls in no conversation, in the basket's order. */
export function looseOf(b: Basket): BasketCall[] {
	const inConv = new Set(b.conversations.flatMap((v) => v.calls.map((c) => c.id)));
	return b.calls.filter((c) => !inConv.has(c.id));
}

/** What a download takes: the whole basket, or one conversation. */
export type Scope = { kind: 'basket' } | { kind: 'conversation'; id: string };

/** When a scope was last downloaded. */
export function lastOf(b: Basket, s: Scope): number | undefined {
	return s.kind === 'basket' ? b.last : b.conversations.find((v) => v.id === s.id)?.last;
}

/**
 * The basket as downloaded now: its last download is now. A whole basket's
 * download is each conversation's too.
 */
export function withDownloaded(b: Basket, s: Scope, now: number): Basket {
	if (s.kind === 'basket') return { ...b, last: now, conversations: b.conversations.map((v) => ({ ...v, last: now })) };
	return { ...b, conversations: b.conversations.map((v) => (v.id === s.id ? { ...v, last: now } : v)) };
}

/** A zip's list (ADR 0015): loose calls, and conversations with their calls in order. */
export interface ZipList {
	calls: string[];
	conversations: { name: string; note: string; calls: string[] }[];
}

/**
 * What a download of a scope takes: its calls that can be downloaded
 * (ok: available and recorded), in the basket's order, and only those put
 * in it since its last download when newOnly. A call in a conversation is
 * in its conversation, not loose. A conversation left with none is left
 * out.
 */
export function listOf(b: Basket, s: Scope, ok: (id: string) => boolean, newOnly = false): ZipList {
	const since = newOnly ? (lastOf(b, s) ?? -1) : -1;
	const take = (cs: BasketCall[]) => cs.filter((c) => c.added > since && ok(c.id)).map((c) => c.id);
	const convs = (s.kind === 'basket' ? b.conversations : b.conversations.filter((v) => v.id === s.id))
		.map((v) => ({ name: v.name, note: v.note ?? '', calls: take(v.calls) }))
		.filter((v) => v.calls.length);
	if (s.kind === 'conversation') return { calls: [], conversations: convs };
	// A call is put in a conversation no sooner than in the basket, so one
	// added since is in its conversations' lists too.
	return { calls: take(looseOf(b)), conversations: convs };
}

export const callsIn = (l: ZipList): number => new Set([...l.calls, ...l.conversations.flatMap((v) => v.calls)]).size;

// The zip's GET URL (ADR 0015): base64url, without padding, of
//
//   version 1 byte (1) · part uvarint · parts uvarint · n uvarint ·
//   n × 16 bytes (the calls: loose first, then the conversations' as they
//   first appear) · m uvarint · m × (name, note: uvarint length + UTF-8;
//   k uvarint; k × uvarint index into the calls)
//
// public.ZipURL (internal/public/zippack.go) is the reference encoder; the
// rules test checks this one against ADR 0015's example.

/** The longest zip URL the server takes, path and query (public.MaxZipURL). */
export const MAX_ZIP_URL = 7680;
/** The most calls one zip holds (R72). */
export const MAX_ZIP_CALLS = 500;
/** The most parts a basket is split into (public.MaxZipParts). */
export const MAX_ZIP_PARTS = 100;
export const ZIP_PREFIX = '/api/v1/zip/';

function uvarint(out: number[], v: number) {
	while (v >= 0x80) {
		out.push((v & 0x7f) | 0x80);
		v = Math.floor(v / 128);
	}
	out.push(v);
}

/** The list as the server reads it: each call once, loose or in conversations, never both. */
function canonical(l: ZipList): { ids: string[]; loose: string[]; convs: { name: string; note: string; calls: string[] }[] } {
	const inConv = new Set<string>();
	const convs = l.conversations
		.map((v) => {
			const calls = [...new Set(v.calls.map((c) => c.toLowerCase()))];
			calls.forEach((c) => inConv.add(c));
			return { name: v.name, note: v.note, calls };
		})
		.filter((v) => v.calls.length);
	const loose = [...new Set(l.calls.map((c) => c.toLowerCase()))].filter((c) => !inConv.has(c));
	const ids = [...loose];
	const seen = new Set(ids);
	for (const v of convs) for (const c of v.calls) if (!seen.has(c)) (seen.add(c), ids.push(c));
	return { ids, loose, convs };
}

/** The packed bytes of a list, for one part of parts (0 and 0: the whole). */
export function pack(l: ZipList, part = 0, parts = 0): Uint8Array {
	const { ids, convs } = canonical(l);
	const out: number[] = [1];
	uvarint(out, part);
	uvarint(out, parts);
	uvarint(out, ids.length);
	const index = new Map<string, number>();
	ids.forEach((id, i) => {
		index.set(id, i);
		const hex = id.replaceAll('-', '');
		for (let j = 0; j < 32; j += 2) out.push(parseInt(hex.slice(j, j + 2), 16));
	});
	uvarint(out, convs.length);
	const enc = new TextEncoder();
	for (const v of convs) {
		for (const s of [v.name, v.note]) {
			const b = enc.encode(s);
			uvarint(out, b.length);
			for (const x of b) out.push(x);
		}
		uvarint(out, v.calls.length);
		for (const c of v.calls) uvarint(out, index.get(c)!);
	}
	return Uint8Array.from(out);
}

/** base64url, without padding. */
export function base64url(b: Uint8Array): string {
	let s = '';
	for (const x of b) s += String.fromCharCode(x);
	return btoa(s).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/, '');
}

/**
 * A packed list read back, with its part and parts: a shared conversation's
 * link (ADR 0020) is one. Undefined unless s is exactly what pack writes
 * for some list (as the server reads a zip URL, ADR 0015): one list, one
 * string. It reads what anyone may have typed, so every length is bounded
 * before it's used.
 */
export function unpack(s: string): { list: ZipList; part: number; parts: number } | undefined {
	if (!/^[A-Za-z0-9_-]+$/.test(s) || s.length % 4 === 1) return undefined;
	let raw: string;
	try {
		raw = atob(s.replaceAll('-', '+').replaceAll('_', '/') + '='.repeat((4 - (s.length % 4)) % 4));
	} catch {
		return undefined;
	}
	const b = Uint8Array.from(raw, (c) => c.charCodeAt(0));
	let at = 0;
	const varint = (max: number): number | undefined => {
		let v = 0;
		for (let shift = 1; at < b.length && shift < 2 ** 35; shift *= 128) {
			const x = b[at++];
			v += (x & 0x7f) * shift;
			if (!(x & 0x80)) return v <= max ? v : undefined;
		}
		return undefined;
	};
	const text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true });
	const string = (max: number): string | undefined => {
		const n = varint(max);
		if (n === undefined || at + n > b.length) return undefined;
		try {
			return text.decode(b.subarray(at, (at += n)));
		} catch {
			return undefined;
		}
	};
	if (b[at++] !== 1) return undefined;
	const part = varint(MAX_ZIP_PARTS);
	const parts = varint(MAX_ZIP_PARTS);
	const n = varint(MAX_ZIP_CALLS);
	if (part === undefined || parts === undefined || n === undefined || at + 16 * n > b.length) return undefined;
	const ids: string[] = [];
	for (let i = 0; i < n; i++) {
		const hex = Array.from(b.subarray(at, (at += 16)), (x) => x.toString(16).padStart(2, '0')).join('');
		ids.push(`${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`);
	}
	const m = varint(MAX_ZIP_CALLS);
	if (m === undefined) return undefined;
	const conversations: ZipList['conversations'] = [];
	const inConv = new Set<string>();
	for (let i = 0; i < m; i++) {
		const name = string(MAX_NAME);
		const note = string(MAX_NOTE);
		const k = varint(n);
		if (name === undefined || note === undefined || !k) return undefined;
		const calls: string[] = [];
		for (let j = 0; j < k; j++) {
			const ix = varint(n - 1);
			if (ix === undefined) return undefined;
			calls.push(ids[ix]);
			inConv.add(ids[ix]);
		}
		conversations.push({ name, note, calls });
	}
	if (at !== b.length) return undefined;
	const list: ZipList = { calls: ids.filter((id) => !inConv.has(id)), conversations };
	// Anything pack wouldn't write (a call twice, out of order, a longer
	// varint than needed, stray bits in the last character) is refused.
	return base64url(pack(list, part, parts)) === s ? { list, part, parts } : undefined;
}

/** A list's zip URL, for one part of parts (0 and 0: the whole). */
export const zipURL = (l: ZipList, part = 0, parts = 0): string => ZIP_PREFIX + base64url(pack(l, part, parts));

/** How long a list's URL is: what a part's size is checked against. */
const urlLength = (l: ZipList): number => ZIP_PREFIX.length + Math.ceil((pack(l, 1, 1).length * 4) / 3);

const fits = (l: ZipList): boolean => callsIn(l) <= MAX_ZIP_CALLS && urlLength(l) <= MAX_ZIP_URL;

/** One part of a download: its number of how many, and its URL. */
export interface ZipPart {
	part: number;
	parts: number;
	url: string;
	calls: number;
}

/**
 * A download's zips (R65, R72; ADR 0015): one, or "part n of N" when the
 * list is too big for one URL or over 500 calls. Calls go in the list's
 * order, loose calls first, a conversation's calls kept together where
 * they fit in a part of their own; a conversation split across parts
 * carries its name and note in each. None: nothing to download.
 */
export function zipParts(l: ZipList): ZipPart[] {
	const { loose, convs } = canonical(l);
	const out: ZipList[] = [];
	let cur: ZipList = { calls: [], conversations: [] };
	const empty = (p: ZipList) => p.calls.length === 0 && p.conversations.length === 0;
	const close = () => {
		if (!empty(cur)) out.push(cur);
		cur = { calls: [], conversations: [] };
	};
	for (const id of loose) {
		const next = { ...cur, calls: [...cur.calls, id] };
		if (fits(next)) cur = next;
		else {
			close();
			cur = { calls: [id], conversations: [] };
		}
	}
	for (const v of convs) {
		const whole = { ...cur, conversations: [...cur.conversations, v] };
		if (fits(whole)) {
			cur = whole;
			continue;
		}
		const alone: ZipList = { calls: [], conversations: [v] };
		if (!empty(cur) && fits(alone)) {
			close();
			cur = alone;
			continue;
		}
		// Too big for a part of its own: call by call, with its name and note
		// in each part it's in. Its piece in the part is the part's last.
		let piece: ZipList['conversations'][number] | undefined;
		for (const id of v.calls) {
			const grown = piece ? { ...piece, calls: [...piece.calls, id] } : { name: v.name, note: v.note, calls: [id] };
			const next: ZipList = { ...cur, conversations: [...(piece ? cur.conversations.slice(0, -1) : cur.conversations), grown] };
			if (fits(next)) {
				cur = next;
				piece = grown;
			} else {
				close();
				piece = { name: v.name, note: v.note, calls: [id] };
				cur = { calls: [], conversations: [piece] };
			}
		}
	}
	close();
	if (out.length === 1) return [{ part: 0, parts: 0, url: zipURL(out[0]), calls: callsIn(out[0]) }];
	return out.slice(0, MAX_ZIP_PARTS).map((p, i) => ({ part: i + 1, parts: out.length, url: zipURL(p, i + 1, out.length), calls: callsIn(p) }));
}

// The countdown (R64; ADR 0018): from the moment the call details say
// retention will delete the call, so the browser needn't know the setting.

const HOUR = 3_600_000;
const DAY = 24 * HOUR;

/** "deleted in 3 days", "deleted in 5 hours"; undefined when the server didn't say. */
export function countdownOf(deletesAt: string | null | undefined, now: number): string | undefined {
	if (!deletesAt) return undefined;
	const left = Date.parse(deletesAt) - now;
	if (Number.isNaN(left)) return undefined;
	if (left <= 0) return 'deleted at the next nightly run';
	const days = Math.floor(left / DAY);
	if (days >= 1) return `deleted in ${days} ${days === 1 ? 'day' : 'days'}`;
	const hours = Math.max(1, Math.ceil(left / HOUR));
	return `deleted in ${hours} ${hours === 1 ? 'hour' : 'hours'}`;
}

/** A new conversation's ID: random, short, never shown. */
export function conversationID(): string {
	const b = crypto.getRandomValues(new Uint8Array(9));
	return base64url(b);
}
