// The Systems page's data and rules (SPEC.md R11, R12, R21, R22; ADR 0003,
// 0006, 0034): systems, their attachments and the pairs that aren't
// attached, and the settings of systems, tags and groups, with the form
// that edits any of them, checked and turned into what's sent: the whole
// thing to create one, only what changed to edit one (PATCH).
import type { Label } from './talkgroups.ts';

/** A system as GET /systems gives it (ADR 0003, 0006). */
export interface FullSystem {
	id: number;
	label: string;
	name: string;
	auto_create: boolean;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
}

/** GET /attachments: a recorder + shortName pair and its system (R12). */
export interface Attachment {
	recorder: string;
	short_name: string;
	system_id: number;
	system_label: string;
	attached_at?: string;
}

/** GET /rejections' pairs: calls refused for not being attached, in R12's words. */
export interface Unattached {
	recorder: string;
	short_name: string;
	calls: number;
	last_at: string | null;
	message: string;
}

export type Kind = 'system' | 'tag' | 'group';

/** A pair's key. */
export const pairOf = (p: { recorder: string; short_name: string }) => `${p.recorder}/${p.short_name}`;

/** The pairs with rejected calls that aren't attached now (attaching keeps only calls after it, so the count stays). */
export const unattached = (rejected: Unattached[], attachments: Attachment[]) => {
	const on = new Set(attachments.map(pairOf));
	return rejected.filter((p) => !on.has(pairOf(p)));
};

/** The form's fields, as typed. A tag or group has no ID, label or auto-create. */
export interface Form {
	id: string;
	label: string;
	name: string;
	auto_create: boolean;
	/** '' is not set (null). */
	alert: string;
	led_colour: string;
	sort_order: string;
}

export type Field = keyof Form;

export const fieldOrder: Field[] = ['id', 'label', 'name', 'auto_create', 'alert', 'led_colour', 'sort_order'];

type Thing = FullSystem | Label;

export const isSystem = (t: Thing | undefined): t is FullSystem => !!t && 'label' in t;

/** The form for a thing, or for a new one of its kind. */
export function formOf(t: Thing | undefined): Form {
	return {
		id: t && isSystem(t) ? String(t.id) : '',
		label: t && isSystem(t) ? t.label : '',
		name: t?.name ?? '',
		auto_create: t && isSystem(t) ? t.auto_create : false,
		alert: t?.alert ?? '',
		led_colour: t?.led_colour ?? '',
		sort_order: String(t?.sort_order ?? 0)
	};
}

/** What's sent: the whole new thing, or only what changed. */
export type Body = Partial<{
	id: number;
	label: string;
	name: string;
	auto_create: boolean;
	alert: string | null;
	led_colour: string | null;
	sort_order: number;
}>;

export type Checked = { problems: Partial<Record<Field, string>> } | { body: Body };

const tooLong = (s: string, max: number) => new TextEncoder().encode(s).length > max;
const INT32 = 2 ** 31;

/**
 * Checks the form as ArchiveUx section 5 asks and gives what to send. was
 * is the thing as it is, or undefined for a new one.
 */
export function check(kind: Kind, f: Form, was: Thing | undefined): Checked {
	const p: Partial<Record<Field, string>> = {};
	const label = f.label.trim();
	const name = f.name.trim();
	const sort = f.sort_order.trim();
	if (kind === 'system') {
		if (!was) {
			const id = f.id.trim();
			if (!/^[1-9]\d{0,15}$/.test(id) || Number(id) > Number.MAX_SAFE_INTEGER) p.id = 'Enter a whole number from 1, such as 1. It is fixed once the system is made.';
		}
		if (!label) p.label = 'Give it a label, such as EX.';
		else if (tooLong(label, 100)) p.label = 'Keep the label to 100 characters.';
		if (tooLong(name, 200)) p.name = 'Keep the name to 200 characters.';
	} else {
		if (!name) p.name = `Give the ${kind} a name.`;
		else if (tooLong(name, 100)) p.name = 'Keep the name to 100 characters.';
	}
	if (!/^-?\d{1,10}$/.test(sort) || Number(sort) < -INT32 || Number(sort) >= INT32) p.sort_order = 'Enter a whole number, such as 0 or 10.';
	if (Object.keys(p).length) return { problems: p };

	const alert = f.alert === '' ? null : f.alert;
	const led = f.led_colour === '' ? null : f.led_colour;
	const all: Body = { name, alert, led_colour: led, sort_order: Number(sort) };
	if (kind === 'system') Object.assign(all, { label, auto_create: f.auto_create });
	if (!was) {
		if (kind === 'system') all.id = Number(f.id.trim());
		return { body: all };
	}
	const body: Body = {};
	for (const [k, v] of Object.entries(all) as [keyof Body, unknown][]) {
		if ((was as unknown as Record<string, unknown>)[k] !== v) (body as Record<string, unknown>)[k] = v;
	}
	return { body };
}

/** Which field a refusal from the admin API is about, by its code (R78), if one. */
export function fieldOfError(code: string): Field | undefined {
	switch (code) {
		case 'invalid_id':
		case 'system_exists':
			return 'id';
		case 'invalid_label':
		case 'label_taken':
			return 'label';
		case 'invalid_name':
		case 'name_required':
		case 'group_exists':
		case 'tag_exists':
			return 'name';
		case 'invalid_alert':
			return 'alert';
		case 'invalid_led_colour':
			return 'led_colour';
	}
	return undefined;
}

/** A setting's name as the tables show it: "none" is a setting of its own, null isn't set. */
export const settingWords = (v: string | null) => (v === null ? '' : v === 'none' ? 'None' : v);
