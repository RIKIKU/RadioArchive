// Unit search for the Recent calls page's unit filter (SPEC.md R26, R27,
// R46; ADR 0019): radios by alias or radio ID, and range aliases by alias
// or radio IDs, from the server, which finds only radios heard on calls the
// public may hear, and ranges with one of those in them. Suggest asks for
// its suggestions as the listener types, at once, so the answers are kept
// by query and the box is told to look again (refresh) when one arrives.

export const UNIT_SEARCH_PATH = '/api/v1/units/search';

/**
 * A radio unit search found: alias is its own, null when it has none; in,
 * for a radio with no unit of its own, is the range alias that covers it.
 */
export interface FoundRadio {
	system?: number;
	id: number;
	alias: string | null;
	in?: string;
}

/** A range alias unit search found: its radio IDs, first to last. */
export interface FoundRange {
	system?: number;
	first: number;
	last: number;
	alias: string | null;
}

/** What unit search finds. system is given only when there are several systems. */
export type FoundUnit = FoundRadio | FoundRange;

const isRange = (u: FoundUnit): u is FoundRange => 'first' in u;

/** A unit's radio IDs as the API's unit parameter takes them: `5001`, or a range's `5100-5199`. */
export const unitSpan = (u: FoundUnit): string => (isRange(u) ? `${u.first}-${u.last}` : String(u.id));

/** A unit as the filter names it: `[system.]id` or `[system.]first-last`, as the API's unit parameter takes it. */
export const unitKey = (u: FoundUnit): string => (u.system === undefined ? unitSpan(u) : `${u.system}.${unitSpan(u)}`);

/**
 * A unit as the suggestions and the box show it: "Engine 1 (5001)",
 * "Portables (5100-5199)" for a range, "5150 in Portables" for a radio only
 * a range names, else its radio ID alone.
 */
export function unitLabel(u: FoundUnit): string {
	if (isRange(u)) return u.alias ? `${u.alias} (${unitSpan(u)})` : unitSpan(u);
	if (u.alias) return `${u.alias} (${u.id})`;
	return u.in ? `${u.id} in ${u.in}` : String(u.id);
}

/** Unit search's answers by query, fetched as they're first asked for. */
export class UnitSearch {
	private found = new Map<string, FoundUnit[]>();
	private asked = new Set<string>();
	private timer: ReturnType<typeof setTimeout> | undefined;

	/** onfound: an answer arrived, so the suggestions may be worked out again. */
	constructor(
		private onfound: () => void,
		private fetcher: typeof fetch = (...a) => fetch(...a)
	) {}

	private static norm(q: string): string {
		return q.trim().toLowerCase();
	}

	/** The units found for q, or undefined while they're on their way (and asked for, after a short pause in typing). */
	for(q: string): FoundUnit[] | undefined {
		const k = UnitSearch.norm(q);
		if (!k) return [];
		const got = this.found.get(k);
		if (got || this.asked.has(k)) return got;
		clearTimeout(this.timer);
		this.timer = setTimeout(() => void this.ask(k), 150);
		return undefined;
	}

	/** The units found for q, waiting for them if need be. */
	async get(q: string): Promise<FoundUnit[]> {
		const k = UnitSearch.norm(q);
		if (!k) return [];
		return this.found.get(k) ?? (await this.ask(k));
	}

	private async ask(k: string): Promise<FoundUnit[]> {
		this.asked.add(k);
		try {
			const r = await this.fetcher(`${UNIT_SEARCH_PATH}?q=${encodeURIComponent(k)}`, { headers: { Accept: 'application/json' } });
			if (!r.ok) throw new Error(String(r.status));
			const units = ((await r.json()) as { units: FoundUnit[] }).units;
			this.found.set(k, units);
			this.onfound();
			return units;
		} catch {
			// Asked again next time it's typed.
			this.asked.delete(k);
			return [];
		}
	}
}
