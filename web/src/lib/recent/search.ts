// The Recent calls page's filters and its searches (SPEC.md R46, R73;
// ADR 0013, ADR 0016). Nothing here touches the page or the network, so it
// can be tested on its own (tests/recent-rules.spec.ts).
//
// The filters live in the page's address, as the listener chose them, so a
// filtered list can be linked to:
//
//   /recent?talkgroups=1001,1002&from=2026-11-20&from_time=14:00&to=2026-11-21&unit=2001&order=oldest
//
//   talkgroups  none: the listening set (and the session's additions);
//               "all": every public talkgroup; else the share link's items
//               (ADR 0008: `[system "."] id ["-" id]`, joined by commas)
//   from, to    days, YYYY-MM-DD, on the deployment's clock (TZ), as
//               ArchiveUx's DateRange gives them; none: the last 31 days
//   from_time, to_time   HH:MM on the same clock; none: the whole day
//   unit        a radio ID, optionally after its system's ID and a dot
//   order       oldest; none: newest first
//
// The search sent to the API names the talkgroups those resolve to in the
// catalogue, the range as instants (rangeBounds), and the rest as they are.
// A set of talkgroups too long for a URL behind a default nginx goes as a
// POST body instead (ADR 0016); the address keeps only what was chosen.

import { rangeBounds } from 'archiveux/dates.js';
import { decodeSet, keyOf, linkEntries, resolve, type Index, type Span, type Talkgroup } from '#lib/listening/set.ts';
import type { CallInfo } from '#lib/audio/player.svelte.ts';

export const RECENT_PATH = '/api/v1/recent-calls';

/** How many calls a page of the list asks for (at most 100, R73). */
export const PAGE_SIZE = 50;

/**
 * The longest GET a search sends: zips' URL budget (ADR 0015), inside
 * nginx's default 8 KB request line. Longer goes as a POST body.
 */
export const MAX_URL = 7680;

export type Scope = 'set' | 'all' | 'chosen';
export type Order = 'newest' | 'oldest';

/** The filters, as the listener chose them. */
export interface Filters {
	scope: Scope;
	/** The talkgroups chosen (scope chosen), as the address names them. */
	talkgroups: Span[];
	from: string;
	to: string;
	fromTime: string;
	toTime: string;
	unit: string;
	order: Order;
}

export const noFilters = (): Filters => ({ scope: 'set', talkgroups: [], from: '', to: '', fromTime: '', toTime: '', unit: '', order: 'newest' });

const DAY = /^\d{4}-\d{2}-\d{2}$/;
const TIME = /^([01]\d|2[0-3]):[0-5]\d$/;
/** A radio ID, optionally after its system's ID: what the API takes as unit. */
export const UNIT = /^(\d{1,19}\.)?\d{1,19}$/;

/** The filters an address holds. Anything that doesn't read is left out. */
export function readFilters(p: { get(name: string): string | null }): Filters {
	const f = noFilters();
	const tg = p.get('talkgroups');
	if (tg === 'all') f.scope = 'all';
	else if (tg) {
		f.scope = 'chosen';
		f.talkgroups = decodeSet(tg).map((s) => ({ ...s, priority: false }));
	}
	const day = (k: string) => (DAY.test(p.get(k) ?? '') ? p.get(k)! : '');
	const time = (k: string) => (TIME.test(p.get(k) ?? '') ? p.get(k)! : '');
	f.from = day('from');
	f.to = f.from ? day('to') : '';
	if (f.to && f.to < f.from) [f.from, f.to] = [f.to, f.from];
	f.fromTime = f.from ? time('from_time') : '';
	f.toTime = f.from ? time('to_time') : '';
	f.unit = (p.get('unit') ?? '').trim();
	f.order = p.get('order') === 'oldest' ? 'oldest' : 'newest';
	return f;
}

/** The address's query for filters: only what differs from none. */
export function writeFilters(f: Filters): string {
	const parts: string[] = [];
	if (f.scope === 'all') parts.push('talkgroups=all');
	else if (f.scope === 'chosen' && f.talkgroups.length)
		parts.push(`talkgroups=${f.talkgroups.map((s) => `${s.system === undefined ? '' : `${s.system}.`}${s.from}${s.to === s.from ? '' : `-${s.to}`}`).join(',')}`);
	if (f.from) {
		parts.push(`from=${f.from}`);
		if (f.fromTime) parts.push(`from_time=${f.fromTime}`);
		if (f.to && f.to !== f.from) parts.push(`to=${f.to}`);
		if (f.toTime) parts.push(`to_time=${f.toTime}`);
	}
	if (f.unit) parts.push(`unit=${encodeURIComponent(f.unit)}`);
	if (f.order === 'oldest') parts.push('order=oldest');
	return parts.join('&');
}

/**
 * The talkgroups chosen, as the catalogue has them now, in its order: those
 * not public are left out without a word (R33), and a range is read
 * against the catalogue, never expanded (ADR 0008).
 */
export const chosenOf = (ix: Index, f: Filters): Talkgroup[] => resolve(ix, linkEntries(ix, f.talkgroups)).map((r) => r.talkgroup);

/** A talkgroup as the filters name it. */
export const spanOf = (t: Talkgroup): Span => ({ system: t.system, from: t.id, to: t.id, priority: false });

/** A search to send: the API's parameters, or nothing to ask (talkgroups were named, and none is public). */
export interface Search {
	talkgroups?: string[];
	from?: string;
	to?: string;
	unit?: string;
	order?: 'oldest';
}

/**
 * The search the filters stand for. listening is the listening set and the
 * session's additions, as the catalogue has them: the default, and every
 * public talkgroup when it's empty (ADR 0013). Undefined: talkgroups were
 * chosen and none of them is public, so nothing matches, and nothing is
 * asked.
 */
export function searchOf(f: Filters, ix: Index, listening: Talkgroup[], timeZone: string): Search | undefined {
	const s: Search = {};
	if (f.scope === 'chosen') {
		const ts = chosenOf(ix, f);
		if (!ts.length) return undefined;
		s.talkgroups = ts.map(keyOf);
	} else if (f.scope === 'set' && listening.length) {
		s.talkgroups = [...new Set(listening.map(keyOf))];
	}
	const b = rangeBounds({ from: f.from, to: f.to, fromTime: f.fromTime, toTime: f.toTime }, timeZone);
	if (b) {
		s.from = b.start;
		s.to = b.end;
	}
	if (f.unit) s.unit = f.unit;
	if (f.order === 'oldest') s.order = 'oldest';
	return s;
}

/** The request for a page of a search: a GET, or a POST when the URL would be too long. */
export function requestOf(s: Search, cursor?: string, limit = PAGE_SIZE): { url: string; init: RequestInit } {
	const q = new URLSearchParams();
	if (s.talkgroups) q.set('talkgroups', s.talkgroups.join(','));
	if (s.from) q.set('from', s.from);
	if (s.to) q.set('to', s.to);
	if (s.unit) q.set('unit', s.unit);
	if (s.order) q.set('order', s.order);
	if (cursor) q.set('cursor', cursor);
	q.set('limit', String(limit));
	const url = `${RECENT_PATH}?${q}`;
	const headers = { Accept: 'application/json' };
	if (url.length <= MAX_URL) return { url, init: { headers } };
	const body = { ...s, ...(cursor ? { cursor } : {}), limit };
	return { url: RECENT_PATH, init: { method: 'POST', headers: { ...headers, 'Content-Type': 'application/json' }, body: JSON.stringify(body) } };
}

/** A call as recent calls give it: the call details' shape (ADR 0004). */
export interface RecentCall {
	id: string;
	system: { id: number; label: string; name: string };
	talkgroup: { id: number; label: string; name: string; tag: string };
	patched_talkgroups: number[];
	started_at: string;
	ended_at: string | null;
	duration_ms: number | null;
	in_progress: boolean;
	transmissions: { unit: number; offset_ms: number; duration_ms: number | null }[];
	recording: { url: string; type: string; size: number } | null;
}

export interface RecentPage {
	calls: RecentCall[];
	next: string | null;
}

/** A refusal (R78): a stable code and a message. */
export interface Refusal {
	error: string;
	message: string;
}

/** Which field a refusal is about, for its message under that field (ArchiveUx section 5). */
export function fieldOf(code: string): 'dates' | 'unit' | 'talkgroups' | undefined {
	switch (code) {
		case 'range_too_long':
		case 'invalid_range':
		case 'invalid_time':
			return 'dates';
		case 'invalid_unit':
			return 'unit';
		case 'invalid_talkgroups':
		case 'too_many_talkgroups':
			return 'talkgroups';
	}
	return undefined;
}

/** The units heard on a call, each once, in the order heard. */
export const unitsOf = (c: RecentCall): number[] => [...new Set(c.transmissions.map((t) => t.unit))];

/** The catalogue's talkgroup a call is on, for its groups, colour and alert. */
export function talkgroupOf(ix: Index, c: RecentCall): Talkgroup | undefined {
	return ix.single ? ix.byId.get(c.talkgroup.id) : ix.byKey.get(`${c.system.id}.${c.talkgroup.id}`);
}

/** m:ss, or h:mm:ss. */
export function clock(ms: number): string {
	const s = Math.round(ms / 1000);
	const h = Math.floor(s / 3600);
	const m = Math.floor((s % 3600) / 60);
	const ss = String(s % 60).padStart(2, '0');
	return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`;
}

const formats = new Map<string, Intl.DateTimeFormat>();
function fmt(timeZone: string, withYear: boolean): Intl.DateTimeFormat {
	const key = `${timeZone}|${withYear}`;
	let f = formats.get(key);
	if (!f) {
		f = new Intl.DateTimeFormat('en-AU', {
			timeZone,
			weekday: 'short',
			day: 'numeric',
			month: 'short',
			...(withYear ? { year: 'numeric' } : {}),
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit',
			hourCycle: 'h23'
		});
		formats.set(key, f);
	}
	return f;
}

/** A call's start on the deployment's clock (section 13): "Wed 25 Nov, 14:03:22", the year when it isn't this one. */
export function startOf(iso: string, timeZone: string, now = new Date()): string {
	const year = (d: Date) => new Intl.DateTimeFormat('en-AU', { timeZone, year: 'numeric' }).format(d);
	const d = new Date(iso);
	return fmt(timeZone, year(d) !== year(now)).format(d);
}

/** A call's start in full on the deployment's clock, for its call card (R61): "Wednesday 25 November 2026, 14:03:22". */
export function dateTimeOf(iso: string, timeZone: string): string {
	return new Intl.DateTimeFormat('en-AU', {
		timeZone,
		weekday: 'long',
		day: 'numeric',
		month: 'long',
		year: 'numeric',
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit',
		hourCycle: 'h23'
	}).format(new Date(iso));
}

/** A call's talkgroup's name as shown (R24). */
export const nameOf = (c: RecentCall): string => c.talkgroup.name || c.talkgroup.label || String(c.talkgroup.id);

/** What the dock shows of a call replayed from its recording (ADR 0016). */
export function callInfoOf(ix: Index, c: RecentCall): CallInfo {
	const t = talkgroupOf(ix, c);
	const unit = c.transmissions[0];
	return {
		callId: c.id,
		talkgroup: { id: c.talkgroup.id, label: c.talkgroup.label, name: nameOf(c), tag: c.talkgroup.tag, groups: t?.groups ?? [], led_colour: t?.led_colour ?? null, alert: t?.alert ?? null },
		patched: c.patched_talkgroups,
		system: ix.single ? undefined : c.system,
		unit: unit ? { id: unit.unit, name: '' } : null,
		startedAt: c.started_at,
		source: 'recording',
		durationMs: c.duration_ms ?? undefined
	};
}

/** A zone's name as a person reads it: "Australia/Melbourne" is "Melbourne". */
export const zoneLabel = (timeZone: string): string => (timeZone === 'UTC' ? 'UTC' : (timeZone.split('/').pop() ?? timeZone).replaceAll('_', ' '));
