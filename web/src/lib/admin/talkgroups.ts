// The Talkgroups page's data and rules (SPEC.md R17, R21-R24, R50; ADR
// 0006, 0026, 0031): the admin table's rows as GET /admin/talkgroups gives
// them, the search and the filters (in the browser, over every row, with no
// request), where a talkgroup's alert and LED colour come from when it has
// none of its own, and the edit form's fields as typed, checked and turned
// into a PATCH of what changed.

export type State = 'public' | 'hidden' | 'ignored';

/** A talkgroup as the admin table has it (ADR 0026): every R22 field, last heard and calls in the last 30 days. */
export interface TableRow {
	system_id: number;
	id: number;
	label: string;
	name: string;
	tag: string | null;
	groups: string[];
	state: State;
	unreviewed: boolean;
	delay_minutes: number;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
	fireground_channel: number | null;
	type: string;
	frequency: number | null;
	last_heard_at: string | null;
	calls_30_days: number;
	/** Never quiet, as its tag's always show does too (R115; ADR 0054). */
	always_show?: boolean;
	/** Listeners' lists leave it out (R114): not heard in the quiet window while its system was receiving. */
	quiet?: boolean;
	not_heard_days?: number;
}

/** GET /api/v1/admin/talkgroups. */
export interface Table {
	calls_since: string;
	talkgroups: TableRow[];
}

/** A tag or a group (GET /tags, /groups): its own alert, LED colour and sort order. */
export interface Label {
	id: number;
	name: string;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
	/** A tag's only: its talkgroups are never quiet (R115). */
	always_show?: boolean;
}

/** A system (GET /systems). */
export interface System {
	id: number;
	label: string;
	name: string;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
}

/** GET /rejections' talkgroups (R17; ADR 0003): "TG 4711: 38 calls rejected, not in the list". */
export interface TalkgroupRejection {
	system_id: number;
	talkgroup: number;
	reason: string;
	calls: number;
	message: string;
}

export const states: State[] = ['public', 'hidden', 'ignored'];
export const stateWords: Record<State, string> = { public: 'Public', hidden: 'Hidden', ignored: 'Ignored' };

/** ADR 0006's names; none is a setting of its own ("no alert", "no colour"). */
export const alerts = ['none', 'beep', 'chime', 'pip', 'tone'];
export const ledColours = ['none', 'red', 'orange', 'yellow', 'green', 'cyan', 'blue', 'purple', 'pink'];

export const MAX_DELAY = 1440;
export const MAX_GROUPS = 50;
const MAX_FREQUENCY_HZ = 100_000_000_000;
const INT32 = 2 ** 31;

/** A row's key: a talkgroup ID is its system's. */
export const keyOf = (t: { system_id: number; id: number }) => `${t.system_id}/${t.id}`;

/** R24: the talkgroup's name, else its label, else its ID. */
export const nameOf = (t: { id: number; label: string; name: string }) => t.name || t.label || `TG ${t.id}`;

/** How the page names a talkgroup in buttons and headings: "1001 Dispatch North". */
export const titleOf = (t: { id: number; label: string; name: string }) => (t.name || t.label ? `${t.id} ${nameOf(t)}` : `TG ${t.id}`);

// Search and filters (R50, A23).

export interface Filters {
	/** Searched across ID, label and name, ignoring case. */
	q: string;
	/** A system's ID, or '' for any. */
	system: string;
	/** A group's name, or '' for any. */
	group: string;
	/** A tag's name, or '' for any. */
	tag: string;
	state: '' | State;
	unreviewed: boolean;
	/** Only the quiet talkgroups (R114). */
	quiet: boolean;
	/** "Not heard in N days": whole days as typed, or '' for off. */
	quietDays: string;
}

export const noFilters = (): Filters => ({ q: '', system: '', group: '', tag: '', state: '', unreviewed: false, quiet: false, quietDays: '' });

const DAY_MS = 24 * 60 * 60 * 1000;

/** "Not heard in N days" as a number of days, or undefined when it's empty or not a whole number from 1. */
export function quietDaysOf(typed: string): number | undefined {
	const s = typed.trim();
	if (!/^\d{1,5}$/.test(s) || Number(s) < 1) return undefined;
	return Number(s);
}

/**
 * The rows the search and filters leave, in the table's order. Never heard
 * counts as not heard in any number of days.
 */
export function filterRows(rows: TableRow[], f: Filters, now: number): TableRow[] {
	const q = f.q.trim().toLowerCase();
	const quiet = quietDaysOf(f.quietDays);
	const system = f.system === '' ? undefined : Number(f.system);
	return rows.filter((t) => {
		if (q && !String(t.id).includes(q) && !t.label.toLowerCase().includes(q) && !t.name.toLowerCase().includes(q)) return false;
		if (system !== undefined && t.system_id !== system) return false;
		if (f.group && !t.groups.includes(f.group)) return false;
		if (f.tag && t.tag !== f.tag) return false;
		if (f.state && t.state !== f.state) return false;
		if (f.unreviewed && !t.unreviewed) return false;
		if (f.quiet && !t.quiet) return false;
		if (quiet !== undefined && t.last_heard_at && now - new Date(t.last_heard_at).getTime() < quiet * DAY_MS) return false;
		return true;
	});
}

/** The distinct values of the rows' tags or groups, by name: the filters' choices. */
export function namesIn(rows: TableRow[], of: 'tag' | 'groups'): string[] {
	const seen = new Set<string>();
	for (const t of rows) {
		if (of === 'tag') {
			if (t.tag) seen.add(t.tag);
		} else for (const g of t.groups) seen.add(g);
	}
	return [...seen].sort((a, b) => a.localeCompare(b, 'en-AU', { sensitivity: 'base', numeric: true }));
}

// Settings taken from elsewhere (ADR 0006).

export type Setting = 'alert' | 'led_colour';

/** Where a talkgroup with none of its own takes a setting from, and what it is: undefined when nowhere sets it. */
export interface Taken {
	value: string;
	from: string;
}

/**
 * ADR 0006's order for a talkgroup with no setting of its own: its tag's,
 * then its groups' in the groups' order (sort order, then name), then its
 * system's. Given the tag and groups as the form has them, so the answer
 * follows what's being typed.
 */
export function takenFrom(setting: Setting, tag: string, groups: string[], tags: Label[], allGroups: Label[], system: System | undefined): Taken | undefined {
	const t = tags.find((x) => x.name === tag);
	if (t && t[setting] !== null) return { value: t[setting]!, from: `its tag ${t.name}` };
	const gs = allGroups.filter((g) => groups.includes(g.name)).sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name));
	for (const g of gs) if (g[setting] !== null) return { value: g[setting]!, from: `its group ${g.name}` };
	if (system && system[setting] !== null) return { value: system[setting]!, from: `its system ${system.label}` };
	return undefined;
}

// Frequency: the API's whole hertz, the form's MHz.

/** 851012500 as "851.0125". */
export function mhzOf(hz: number | null): string {
	if (hz === null) return '';
	const whole = Math.floor(hz / 1e6);
	const frac = String(hz % 1e6)
		.padStart(6, '0')
		.replace(/0+$/, '');
	return frac ? `${whole}.${frac}` : String(whole);
}

/** "851.0125" as 851012500, exactly; undefined when it isn't MHz to at most 6 places. */
export function hzOf(mhz: string): number | undefined {
	const m = /^(\d{1,6})(?:\.(\d{1,6}))?$/.exec(mhz.trim());
	if (!m) return undefined;
	const hz = Number(m[1]) * 1e6 + Number((m[2] ?? '').padEnd(6, '0'));
	return hz >= 1 && hz <= MAX_FREQUENCY_HZ ? hz : undefined;
}

// The edit form.

/** The form's fields, as typed. */
export interface Form {
	label: string;
	name: string;
	tag: string;
	groups: string;
	type: string;
	frequency: string;
	sort_order: string;
	delay_minutes: string;
	/** '' is "taken from elsewhere" (null). */
	alert: string;
	led_colour: string;
	fireground_channel: string;
	state: State;
	always_show: boolean;
}

export type Field = keyof Form;

/** The form's fields in the order the form shows them, for the focus on the first problem. */
export const fieldOrder: Field[] = ['label', 'name', 'tag', 'groups', 'state', 'type', 'frequency', 'sort_order', 'delay_minutes', 'alert', 'led_colour', 'fireground_channel', 'always_show'];

export function formOf(t: TableRow): Form {
	return {
		label: t.label,
		name: t.name,
		tag: t.tag ?? '',
		groups: t.groups.join(', '),
		type: t.type,
		frequency: mhzOf(t.frequency),
		sort_order: String(t.sort_order),
		delay_minutes: String(t.delay_minutes),
		alert: t.alert ?? '',
		led_colour: t.led_colour ?? '',
		fireground_channel: t.fireground_channel === null ? '' : String(t.fireground_channel),
		state: t.state,
		always_show: !!t.always_show
	};
}

/** The groups typed, split at commas, trimmed, each once. */
export function groupsOf(typed: string): string[] {
	const out: string[] = [];
	for (const g of typed.split(',').map((s) => s.trim())) if (g && !out.includes(g)) out.push(g);
	return out;
}

/** The PATCH body: only what differs from the row. */
export type Patch = Partial<{
	label: string;
	name: string;
	tag: string;
	groups: string[];
	type: string;
	frequency: number | null;
	sort_order: number;
	delay_minutes: number;
	alert: string | null;
	led_colour: string | null;
	fireground_channel: number | null;
	state: State;
	always_show: boolean;
}>;

/** What the form's check found: each problem under its field, or the PATCH. */
export type Checked = { problems: Partial<Record<Field, string>> } | { patch: Patch };

const tooLong = (s: string, max: number) => [...s].length > max || new TextEncoder().encode(s).length > max;

/** Checks the form as ArchiveUx section 5 asks (each problem in words under its field) and gives what changed. */
export function check(f: Form, t: TableRow): Checked {
	const p: Partial<Record<Field, string>> = {};
	const label = f.label.trim();
	const name = f.name.trim();
	const tag = f.tag.trim();
	const groups = groupsOf(f.groups);
	const type = f.type.trim();
	if (tooLong(label, 100)) p.label = 'Keep the label to 100 characters.';
	if (tooLong(name, 200)) p.name = 'Keep the name to 200 characters.';
	if (!tag) p.tag = 'Give it a tag, such as dispatch or fireground.';
	else if (tooLong(tag, 100)) p.tag = 'Keep the tag to 100 characters.';
	if (groups.length > MAX_GROUPS) p.groups = `A talkgroup is in at most ${MAX_GROUPS} groups.`;
	else if (groups.some((g) => tooLong(g, 100))) p.groups = 'Keep each group to 100 characters.';
	if (tooLong(type, 32)) p.type = 'Keep the type to 32 characters.';
	const freq = f.frequency.trim();
	const hz = freq === '' ? null : hzOf(freq);
	if (hz === undefined) p.frequency = 'Enter the frequency in MHz, such as 851.0125, or leave it empty.';
	const sort = f.sort_order.trim();
	if (!/^-?\d{1,10}$/.test(sort) || Number(sort) < -INT32 || Number(sort) >= INT32) p.sort_order = 'Enter a whole number, such as 0 or 10.';
	const delay = f.delay_minutes.trim();
	if (!/^\d{1,4}$/.test(delay) || Number(delay) > MAX_DELAY) p.delay_minutes = `Enter whole minutes from 0 to ${MAX_DELAY.toLocaleString('en-AU')}; 0 is no delay.`;
	const fg = f.fireground_channel.trim();
	if (fg !== '' && (!/^\d{1,9}$/.test(fg) || Number(fg) >= INT32)) p.fireground_channel = 'Enter a whole number, such as 41, or leave it empty.';
	if (Object.keys(p).length) return { problems: p };

	const patch: Patch = {};
	if (label !== t.label) patch.label = label;
	if (name !== t.name) patch.name = name;
	if (tag !== (t.tag ?? '')) patch.tag = tag;
	if (groups.join('\n') !== t.groups.join('\n')) patch.groups = groups;
	if (type !== t.type) patch.type = type;
	if (hz !== t.frequency) patch.frequency = hz;
	if (Number(sort) !== t.sort_order) patch.sort_order = Number(sort);
	if (Number(delay) !== t.delay_minutes) patch.delay_minutes = Number(delay);
	const alert = f.alert === '' ? null : f.alert;
	if (alert !== t.alert) patch.alert = alert;
	const led = f.led_colour === '' ? null : f.led_colour;
	if (led !== t.led_colour) patch.led_colour = led;
	const fgn = fg === '' ? null : Number(fg);
	if (fgn !== t.fireground_channel) patch.fireground_channel = fgn;
	if (f.state !== t.state) patch.state = f.state;
	if (f.always_show !== !!t.always_show) patch.always_show = f.always_show;
	return { patch };
}

/** Which field a refusal from the admin API is about, by its code (R78), if one. */
export function fieldOfError(code: string): Field | undefined {
	switch (code) {
		case 'invalid_label':
			return 'label';
		case 'invalid_name':
			return 'name';
		case 'invalid_tag':
		case 'tag_required':
			return 'tag';
		case 'invalid_group':
		case 'too_many_groups':
			return 'groups';
		case 'invalid_type':
			return 'type';
		case 'invalid_frequency':
			return 'frequency';
		case 'invalid_delay_minutes':
			return 'delay_minutes';
		case 'invalid_alert':
			return 'alert';
		case 'invalid_led_colour':
			return 'led_colour';
		case 'fireground_channel_taken':
			return 'fireground_channel';
		case 'invalid_state':
			return 'state';
	}
	return undefined;
}

/**
 * Whether a change of state asks first (ADR 0031): ignoring refuses the
 * talkgroup's calls from then on, which can't be got back, and making a
 * hidden or ignored talkgroup public shows its recordings to everyone at
 * once. Hiding doesn't ask: it only takes away, and is undone in a click.
 */
export const asksFirst = (from: State, to: State) => from !== to && (to === 'ignored' || to === 'public');
