// Unit search for the Recent calls page's unit filter (SPEC.md R26, R27,
// R46; ADR 0019): units by alias or radio ID, from the server, which finds
// only units heard on calls the public may hear. Suggest asks for its
// suggestions as the listener types, at once, so the answers are kept by
// query and the box is told to look again (refresh) when one arrives.

export const UNIT_SEARCH_PATH = '/api/v1/units/search';

/** A unit unit search found. system is given only when there are several systems. */
export interface FoundUnit {
	system?: number;
	id: number;
	alias: string | null;
}

/** A unit as the filter names it: `[system.]id`, as the API's unit parameter takes it. */
export const unitKey = (u: FoundUnit): string => (u.system === undefined ? String(u.id) : `${u.system}.${u.id}`);

/** A unit as the box shows it once picked: "Engine 1 (5001)", or its ID alone when it has no alias. */
export const unitLabel = (u: FoundUnit): string => (u.alias ? `${u.alias} (${u.id})` : String(u.id));

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
