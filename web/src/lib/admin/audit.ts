// The Audit log page's data and words (SPEC.md R54, R84; ADR 0029, 0032):
// GET /api/v1/admin/audit's entries, its filters as the page's URL and the
// API's query, and each change put into words ("Label DISP N → DISP North",
// "State public → hidden", "Bench / ex attached to EX"). The API gives the
// fields; the words are the page's (ADR 0029, "The page").
//
// The page's URL holds its filters, so a filtered view can be linked:
//
//   /admin/audit?who=<account ID>&kind=talkgroup&thing=1/1001&from=2026-11-20&to=2026-11-25&order=oldest
//
//   who      an account's ID (GET /audit/admins), or "system"
//   kind     one of kinds
//   thing    which one, with its kind (an entry's thing)
//   from, to YYYY-MM-DD days on the deployment's clock (ArchiveUx's
//            DateRange); to includes its whole day
//   order    oldest; none is newest first
import { rangeBounds } from 'archiveux/dates.js';

/** An entry's admin: their account's ID, and their name and email as they were. */
export interface AuditWho {
	id: string;
	name: string;
	email: string;
}

/** One field's change; null before for a creation, null after for a deletion. */
export interface FieldChange {
	field: string;
	before: unknown;
	after: unknown;
}

export interface AuditEntry {
	id: number;
	at: string;
	who: AuditWho;
	via: string;
	kind: string;
	thing: string;
	label: string;
	action: 'created' | 'changed' | 'deleted';
	changes: FieldChange[];
}

/** GET /api/v1/admin/audit. */
export interface AuditPage {
	entries: AuditEntry[];
	next: string | null;
}

/** The kinds of thing, in the API's order, in words. */
export const kinds: { value: string; label: string }[] = [
	{ value: 'system', label: 'System' },
	{ value: 'talkgroup', label: 'Talkgroup' },
	{ value: 'unit', label: 'Unit' },
	{ value: 'group', label: 'Group' },
	{ value: 'tag', label: 'Tag' },
	{ value: 'attachment', label: 'Attachment' },
	{ value: 'recorder', label: 'Recorder' },
	{ value: 'retention', label: 'Retention' },
	{ value: 'account', label: 'Account' },
	{ value: 'audit', label: 'Audit log' }
];

export const kindOf = (kind: string): string => kinds.find((k) => k.value === kind)?.label ?? kind;

/** How a change was made, in words. */
export function viaOf(via: string): string {
	switch (via) {
		case 'browser':
			return 'Browser';
		case 'module':
			return 'PowerShell module';
		case 'import':
			return 'Import';
		case 'nightly':
			return 'Nightly run';
		default:
			return via;
	}
}

/** Who, in words: the name, else the email, else the account's ID. */
export const whoOf = (w: AuditWho): string => w.name || w.email || w.id;

/** The filters, as the page's URL holds them. */
export interface AuditFilters {
	who: string;
	kind: string;
	thing: string;
	from: string;
	to: string;
	oldest: boolean;
}

export const noFilters = (): AuditFilters => ({ who: '', kind: '', thing: '', from: '', to: '', oldest: false });

const day = /^\d{4}-\d{2}-\d{2}$/;

/** The filters in a URL's query; anything not understood is left out. */
export function readFilters(q: { get(name: string): string | null }): AuditFilters {
	const f = noFilters();
	f.who = q.get('who') ?? '';
	const kind = q.get('kind') ?? '';
	if (kinds.some((k) => k.value === kind)) f.kind = kind;
	// A thing is one kind's: without its kind it means nothing.
	if (f.kind) f.thing = q.get('thing') ?? '';
	const from = q.get('from') ?? '';
	const to = q.get('to') ?? '';
	if (day.test(from)) {
		f.from = from;
		if (day.test(to) && to >= from) f.to = to;
	}
	f.oldest = q.get('order') === 'oldest';
	return f;
}

/** The page's query for the filters ('' for none). */
export function writeFilters(f: AuditFilters): string {
	const q = new URLSearchParams();
	if (f.who) q.set('who', f.who);
	if (f.kind) q.set('kind', f.kind);
	if (f.kind && f.thing) q.set('thing', f.thing);
	if (f.from) q.set('from', f.from);
	if (f.from && f.to) q.set('to', f.to);
	if (f.oldest) q.set('order', 'oldest');
	const s = q.toString();
	return s ? `?${s}` : '';
}

/** The API's query for a page of entries with the filters, the days as instants on the deployment's clock. */
export function requestOf(f: AuditFilters, timeZone: string, limit: number, cursor?: string): string {
	const q = new URLSearchParams();
	q.set('limit', String(limit));
	if (f.who) q.set('who', f.who);
	if (f.kind) q.set('kind', f.kind);
	if (f.kind && f.thing) q.set('thing', f.thing);
	const b = rangeBounds({ from: f.from, to: f.to }, timeZone);
	if (b) {
		q.set('since', b.start);
		q.set('until', b.end);
	}
	if (f.oldest) q.set('order', 'oldest');
	if (cursor) q.set('cursor', cursor);
	return `/audit?${q.toString()}`;
}

/** The fields' names in words, where the API's name isn't already words. */
const fieldNames: Record<string, string> = {
	auto_create: 'Auto-create',
	led_colour: 'LED colour',
	sort_order: 'Sort order',
	delay_minutes: 'Delay (minutes)',
	fireground_channel: 'Fireground channel',
	system_id: 'System',
	system_label: 'System label',
	short_name: 'Short name',
	quiet_after_minutes: 'Quiet after (minutes)',
	id: 'ID',
	from: 'From unit',
	to: 'To unit'
};

export function fieldOf(field: string): string {
	if (fieldNames[field]) return fieldNames[field];
	const s = field.replaceAll('_', ' ');
	return s.charAt(0).toUpperCase() + s.slice(1);
}

/**
 * A part of a change in words: plain text, or a value as typed (a name, a
 * label: shown as code, as the issue writes "Label `DISP N`"), or none.
 */
export type Part = { text: string } | { code: string };

/** Fields whose values are a choice from a few words, not text an admin typed. */
const chosen = new Set(['state', 'role', 'action']);

/** A value in words. */
export function valueOf(field: string, v: unknown): Part {
	if (v === null || v === undefined || v === '') return { text: 'none' };
	if (typeof v === 'boolean') return { text: v ? 'on' : 'off' };
	// As the API has it: IDs and minutes alike, with no separators.
	if (typeof v === 'number') return { text: String(v) };
	if (typeof v === 'string') return chosen.has(field) ? { text: v } : { code: v };
	if (Array.isArray(v)) return v.length ? { code: v.map((x) => (typeof x === 'string' ? x : JSON.stringify(x))).join(', ') } : { text: 'none' };
	return { code: JSON.stringify(v) };
}

/** One line of a change: its parts in order. */
export type Line = Part[];

const plural = (n: number, one: string, many = `${one}s`) => `${n.toLocaleString('en-AU')} ${n === 1 ? one : many}`;

const field = (e: AuditEntry, name: string) => e.changes.find((c) => c.field === name);

/** A day in words on the deployment's clock: "25 Nov 2024". */
export function dayOf(iso: string, timeZone: string): string {
	return new Intl.DateTimeFormat('en-AU', { timeZone, day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(iso));
}

/**
 * An entry's change in words, a line each:
 * - a change, each field: "Label DISP N → DISP North";
 * - a creation, "Created", then each field set: "Name Dispatch North";
 * - a deletion, "Deleted", then each field it had;
 * - an attachment: "Bench / ex attached to EX", "… moved from EX to EX-2", "… detached from EX";
 * - ending an account's sessions: "Signed out everywhere: 2 sessions ended";
 * - the nightly prune: "Deleted 12 entries from before 25 Nov 2024 (kept 730 days)".
 */
export function linesOf(e: AuditEntry, timeZone: string): Line[] {
	if (e.kind === 'audit') {
		const n = Number(field(e, 'entries')?.before ?? 0);
		const before = field(e, 'before')?.before;
		const days = Number(field(e, 'days')?.before ?? 0);
		return [
			[
				{
					text:
						`Deleted ${plural(n, 'entry', 'entries')}` +
						(typeof before === 'string' ? ` from before ${dayOf(before, timeZone)}` : '') +
						(days ? ` (kept ${plural(days, 'day')})` : '')
				}
			]
		];
	}
	if (e.kind === 'attachment') {
		const sys = field(e, 'system_label');
		const pair: Part[] = [{ text: `${e.label} ` }];
		if (e.action === 'created') return [[...pair, { text: 'attached to ' }, valueOf('system_label', sys?.after)]];
		if (e.action === 'deleted') return [[...pair, { text: 'detached from ' }, valueOf('system_label', sys?.before)]];
		if (sys) return [[...pair, { text: 'moved from ' }, valueOf('system_label', sys.before), { text: ' to ' }, valueOf('system_label', sys.after)]];
	}
	if (e.kind === 'account') {
		const s = field(e, 'sessions');
		if (s && e.changes.length === 1) return [[{ text: `Signed out everywhere: ${plural(Number(s.before), 'session')} ended` }]];
	}
	if (e.action === 'created' || e.action === 'deleted') {
		const shown = e.changes.filter((c) => {
			const v = e.action === 'created' ? c.after : c.before;
			return v !== null && v !== '' && !(Array.isArray(v) && v.length === 0);
		});
		return [[{ text: e.action === 'created' ? 'Created' : 'Deleted' }], ...shown.map((c) => [{ text: `${fieldOf(c.field)} ` }, valueOf(c.field, e.action === 'created' ? c.after : c.before)])];
	}
	return e.changes.map((c) => [{ text: `${fieldOf(c.field)} ` }, valueOf(c.field, c.before), { text: ' → ' }, valueOf(c.field, c.after)]);
}

/** A line as plain text, for tests and for an accessible name. */
export const lineText = (l: Line): string => l.map((p) => ('code' in p ? p.code : p.text)).join('');
