// The Units page's data and rules (SPEC.md R26, R27; ADR 0019, 0034): every
// unit of every system as the admin API's GET /systems/{id}/units gives
// them, the search and filter (in the browser, over every row), which alias
// a radio ID gets, and the add form's fields as typed and checked.

/** A unit as the admin API has it: one radio (from === to) or a range of them, in its system. */
export interface Unit {
	system_id: number;
	from: number;
	to: number;
	/** '' is a unit with no alias: its radios show by their IDs (R27). */
	alias: string;
}

/** The longest alias, in bytes (the admin API's MaxUnitAlias). */
export const MAX_ALIAS = 100;
/** The highest radio ID the page takes: what a JavaScript number holds exactly (the API takes up to 2^63 - 1). */
const MAX_ID = Number.MAX_SAFE_INTEGER;

/** A row's key: a unit is its system's radio ID or range. */
export const keyOf = (u: { system_id: number; from: number; to: number }) => `${u.system_id}/${nameOf(u)}`;

/** A unit's name as its path has it: "5001", or "5100-5199". */
export const nameOf = (u: { from: number; to: number }) => (u.from === u.to ? String(u.from) : `${u.from}-${u.to}`);

/** How the page names a unit in buttons and headings: "5001 Engine 1", "5100-5199 Portables", "5200". */
export const titleOf = (u: Unit) => (u.alias ? `${nameOf(u)} ${u.alias}` : nameOf(u));

/** How many radios a unit covers. */
export const sizeOf = (u: { from: number; to: number }) => u.to - u.from + 1;

/** A radio ID as typed: digits only, from 1, no leading zero. */
function idOf(s: string): number | undefined {
	if (!/^[1-9]\d{0,15}$/.test(s)) return undefined;
	const n = Number(s);
	return n <= MAX_ID ? n : undefined;
}

/**
 * A unit as typed: "5001" or "5100-5199" (spaces around the dash allowed),
 * or undefined when it isn't one: a range's last must be above its first.
 */
export function parseUnit(typed: string): { from: number; to: number } | undefined {
	const s = typed.trim();
	const m = /^(\d+)\s*-\s*(\d+)$/.exec(s);
	if (m) {
		const [from, to] = [idOf(m[1]), idOf(m[2])];
		if (from === undefined || to === undefined || to <= from) return undefined;
		return { from, to };
	}
	const one = idOf(s);
	return one === undefined ? undefined : { from: one, to: one };
}

/** What alias a radio ID gets in its system, and from which unit (R26; ADR 0019). */
export interface Resolved {
	/** The alias listeners see; '' for none (they see the radio ID). */
	alias: string;
	/** The unit it comes from: the radio's own, or the range; undefined when none covers it. */
	unit?: Unit;
}

/**
 * ADR 0019's rule, as the gate has it (gate.UnitAlias): a radio's own unit,
 * even with no alias; else the narrowest range covering it, the lowest of
 * two as narrow; else none.
 */
export function resolve(units: Unit[], system: number, radio: number): Resolved {
	let best: Unit | undefined;
	for (const u of units) {
		if (u.system_id !== system || radio < u.from || radio > u.to) continue;
		if (u.from === u.to) return { alias: u.alias, unit: u };
		if (!best || sizeOf(u) < sizeOf(best) || (sizeOf(u) === sizeOf(best) && u.from < best.from)) best = u;
	}
	return best ? { alias: best.alias, unit: best } : { alias: '' };
}

/** The answer in words: "5150 shows as Portables, from the range 5100-5199." */
export function resolvedWords(r: Resolved, radio: number): string {
	if (!r.unit) return `${radio} has no alias: listeners see ${radio}.`;
	if (r.unit.from === r.unit.to) return r.alias ? `${radio} shows as ${r.alias}: its own unit.` : `${radio} has a unit of its own with no alias: listeners see ${radio}, whatever range covers it.`;
	return r.alias ? `${radio} shows as ${r.alias}, from the range ${nameOf(r.unit)}.` : `${radio} is in the range ${nameOf(r.unit)}, which has no alias: listeners see ${radio}.`;
}

// Search and filter (R26: as the Talkgroups page's).

export interface Filters {
	/** Searched across radio ID and alias, ignoring case. */
	q: string;
	/** A system's ID, or '' for any. */
	system: string;
}

export const noFilters = (): Filters => ({ q: '', system: '' });

/**
 * The units the search and filter leave, in the table's order. The search
 * is a part of the unit's radio ID or range ("51" finds 5100-5199) or of its
 * alias; a whole radio ID finds the ranges covering it too, so "5150"
 * finds the range 5150 is in.
 */
export function filterUnits(units: Unit[], f: Filters): Unit[] {
	const q = f.q.trim().toLowerCase();
	const radio = /^\d+$/.test(q) ? Number(q) : undefined;
	const system = f.system === '' ? undefined : Number(f.system);
	return units.filter((u) => {
		if (system !== undefined && u.system_id !== system) return false;
		if (!q) return true;
		return nameOf(u).includes(q) || u.alias.toLowerCase().includes(q) || (radio !== undefined && u.from <= radio && radio <= u.to);
	});
}

// The add form and the alias as typed.

const tooLong = (s: string) => new TextEncoder().encode(s).length > MAX_ALIAS;

/** What's wrong with an alias as typed, in words, or undefined. */
export function aliasProblem(alias: string): string | undefined {
	if (tooLong(alias)) return `Keep the alias to ${MAX_ALIAS} characters.`;
	if (alias.includes(';')) return 'An alias can\'t hold ";".';
	if (/\p{Cc}/u.test(alias)) return "An alias can't hold control characters.";
	return undefined;
}

/** Which add-form field a refusal from the admin API is about, by its code (R78), if one. */
export function fieldOfError(code: string): 'unit' | 'alias' | 'system' | undefined {
	switch (code) {
		case 'unit_not_found':
			return 'unit';
		case 'invalid_alias':
			return 'alias';
		case 'system_not_found':
			return 'system';
	}
	return undefined;
}
