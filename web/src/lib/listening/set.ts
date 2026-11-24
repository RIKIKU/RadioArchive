// The listening set (SPEC.md R41, R42, R43, R47; ADR 0008): the talkgroups
// a listener hears, as the browser keeps them, and the share link that
// carries them to another browser. Nothing here touches the page or the
// network, so it can be tested on its own (tests/share-link.spec.ts).

/** A system, in the catalogue only when there are several (R15). */
export interface System {
	id: number;
	label: string;
	name: string;
}

/** A public talkgroup as the catalogue gives it (ADR 0006). */
export interface Talkgroup {
	/** Its system's ID, only when there are several systems. */
	system?: number;
	id: number;
	label: string;
	/** What's shown (R24): the name, else the label, else the ID. */
	name: string;
	tag: string;
	groups: string[];
	led_colour: string | null;
	alert: string | null;
	delay_minutes: number;
	fireground_channel: number | null;
}

/** GET /api/v1/catalogue: every public talkgroup, in the catalogue's order. */
export interface Catalogue {
	systems?: System[];
	tags: string[];
	groups: string[];
	talkgroups: Talkgroup[];
}

/**
 * One talkgroup in a set, as the browser keeps it: its ID, its system's ID
 * only when the deployment has several, and its priority mark (R43).
 */
export interface Entry {
	system?: number;
	id: number;
	priority?: boolean;
}

/** A talkgroup's key: "1001", or "2.1001" when there are several systems. */
export const keyOf = (t: { system?: number; id: number }): string => (t.system === undefined ? String(t.id) : `${t.system}.${t.id}`);

/** The catalogue, looked up by key and by ID. */
export interface Index {
	catalogue: Catalogue;
	byKey: Map<string, Talkgroup>;
	/** The first talkgroup in the catalogue's order with each ID. */
	byId: Map<number, Talkgroup>;
	/** Whether the deployment shows one system (the catalogue names none). */
	single: boolean;
}

export function indexOf(catalogue: Catalogue): Index {
	const byKey = new Map<string, Talkgroup>();
	const byId = new Map<number, Talkgroup>();
	for (const t of catalogue.talkgroups) {
		byKey.set(keyOf(t), t);
		if (!byId.has(t.id)) byId.set(t.id, t);
	}
	return { catalogue, byKey, byId, single: !catalogue.systems?.length };
}

/**
 * The public talkgroup an entry names, or none. An entry kept while there
 * was one system has no system, and names the first talkgroup with its ID
 * once there are several; one kept while there were several names its own,
 * or, once there's one system again, the talkgroup with its ID.
 */
export function find(ix: Index, e: { system?: number; id: number }): Talkgroup | undefined {
	if (e.system === undefined) return ix.byId.get(e.id);
	return ix.byKey.get(keyOf(e)) ?? (ix.single ? ix.byId.get(e.id) : undefined);
}

/** An entry for a catalogue talkgroup. */
export const entryOf = (t: Talkgroup, priority = false): Entry => (priority ? { system: t.system, id: t.id, priority } : { system: t.system, id: t.id });

/**
 * The entries as the catalogue's talkgroups now: each entry once, in its
 * order, under its talkgroup's own key, and only those still public. What
 * isn't public any more is left out without a word (R33).
 */
export function resolve(ix: Index, entries: Entry[]): { talkgroup: Talkgroup; priority: boolean }[] {
	const seen = new Set<string>();
	const out: { talkgroup: Talkgroup; priority: boolean }[] = [];
	for (const e of entries) {
		const t = find(ix, e);
		if (!t || seen.has(keyOf(t))) continue;
		seen.add(keyOf(t));
		out.push({ talkgroup: t, priority: !!e.priority });
	}
	return out;
}

/** Whether a value is an entry the browser could have kept. */
function isEntry(v: unknown): v is Entry {
	if (!v || typeof v !== 'object') return false;
	const e = v as Record<string, unknown>;
	return (
		Number.isSafeInteger(e.id) &&
		(e.id as number) > 0 &&
		(e.system === undefined || (Number.isSafeInteger(e.system) && (e.system as number) > 0)) &&
		(e.priority === undefined || typeof e.priority === 'boolean')
	);
}

/** Entries kept as JSON ({"v":1,"talkgroups":[…]}); anything else is none. */
export function parseKept(text: string | null): Entry[] {
	if (!text) return [];
	try {
		const kept = JSON.parse(text);
		if (kept?.v !== 1 || !Array.isArray(kept.talkgroups)) return [];
		return kept.talkgroups.filter(isEntry).map((e: Entry) => (e.priority ? { system: e.system, id: e.id, priority: true } : { system: e.system, id: e.id }));
	} catch {
		return [];
	}
}

/** Entries as the browser keeps them. */
export const formatKept = (entries: Entry[]): string => JSON.stringify({ v: 1, talkgroups: entries });

// The share link (R41; ADR 0008): /?set=1001,1002p,1010-1015,2.1001
//
//   set   := item ("," item)*
//   item  := [system "."] id ["-" id] ["p"]
//
// Decimal talkgroup IDs; a run of three or more consecutive IDs with the
// same system and mark is written as a range; "p" marks priority; the
// system is written only when the deployment has several.

/** The share link's query parameter. */
export const SHARE_PARAM = 'set';

/** The most items a share link is read for; the rest are left out. */
export const MAX_ITEMS = 5000;

/** The set in a share link's form. */
export function encodeSet(entries: Entry[]): string {
	const sorted = [...entries].sort((a, b) => (a.system ?? 0) - (b.system ?? 0) || a.id - b.id);
	const items: string[] = [];
	for (let i = 0; i < sorted.length; ) {
		let j = i;
		const same = (x: Entry, y: Entry) => x.system === y.system && !!x.priority === !!y.priority;
		while (j + 1 < sorted.length && same(sorted[j + 1], sorted[i]) && sorted[j + 1].id === sorted[j].id + 1) j++;
		const e = sorted[i];
		const at = e.system === undefined ? '' : `${e.system}.`;
		const mark = e.priority ? 'p' : '';
		if (j - i >= 2) {
			items.push(`${at}${e.id}-${sorted[j].id}${mark}`);
			i = j + 1;
		} else {
			items.push(`${at}${e.id}${mark}`);
			i++;
		}
	}
	return items.join(',');
}

/** One item of a share link: a talkgroup ID or a range of them. */
export interface Span {
	system?: number;
	from: number;
	to: number;
	priority: boolean;
}

const item = /^(?:(\d{1,10})\.)?(\d{1,10})(?:-(\d{1,10}))?(p?)$/;
const MAX_ID = 2 ** 31 - 1;

/** A share link's set, as written; an item that isn't one is left out. */
export function decodeSet(text: string): Span[] {
	const out: Span[] = [];
	for (const part of text.split(',').slice(0, MAX_ITEMS)) {
		const m = item.exec(part.trim());
		if (!m) continue;
		const system = m[1] === undefined ? undefined : Number(m[1]);
		const from = Number(m[2]);
		const to = m[3] === undefined ? from : Number(m[3]);
		if (from < 1 || to < from || to > MAX_ID || (system !== undefined && (system < 1 || system > Number.MAX_SAFE_INTEGER))) continue;
		out.push({ system, from, to, priority: m[4] === 'p' });
	}
	return out;
}

/**
 * The public talkgroups a share link names, in the catalogue's order, each
 * marked priority if any item naming it is. A talkgroup that isn't public
 * isn't in the catalogue, so it's dropped silently (R33): a link naming a
 * hidden talkgroup reads exactly as one naming a talkgroup that never
 * existed.
 */
export function linkEntries(ix: Index, spans: Span[]): Entry[] {
	const out: Entry[] = [];
	for (const t of ix.catalogue.talkgroups) {
		let named = false;
		let priority = false;
		for (const s of spans) {
			if (t.id < s.from || t.id > s.to) continue;
			const fits = s.system === undefined ? ix.byId.get(t.id) === t : s.system === t.system || (ix.single && ix.byId.get(t.id) === t);
			if (!fits) continue;
			named = true;
			priority ||= s.priority;
		}
		if (named) out.push(entryOf(t, priority));
	}
	return out;
}

/** Whether two sets hold the same talkgroups with the same marks. */
export function sameSet(ix: Index, a: Entry[], b: Entry[]): boolean {
	const marks = (es: Entry[]) => new Map(resolve(ix, es).map((r) => [keyOf(r.talkgroup), r.priority]));
	const x = marks(a);
	const y = marks(b);
	if (x.size !== y.size) return false;
	for (const [k, p] of x) if (y.get(k) !== p) return false;
	return true;
}

/**
 * Talkgroups matching a search (R42), as the server's search does (ADR
 * 0006): by name, label, tag or decimal ID, ignoring case; those a field
 * names exactly first, then those with a field or a word in it that starts
 * with the query, then those that contain it, each in the catalogue's
 * order. An empty query matches every talkgroup.
 */
export function search(talkgroups: Talkgroup[], query: string): Talkgroup[] {
	const q = query.trim().toLowerCase();
	if (!q) return talkgroups;
	const ranks: Talkgroup[][] = [[], [], []];
	for (const t of talkgroups) {
		const fields = [t.name, t.label, t.tag, String(t.id)].map((f) => f.toLowerCase());
		if (fields.some((f) => f === q)) ranks[0].push(t);
		else if (fields.some((f) => f.startsWith(q) || f.split(/\s+/).some((w) => w.startsWith(q)))) ranks[1].push(t);
		else if (fields.some((f) => f.includes(q))) ranks[2].push(t);
	}
	return ranks.flat();
}
