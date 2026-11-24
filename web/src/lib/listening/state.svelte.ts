// The listener's listening set and the talkgroups added for the session
// (SPEC.md R41, R43, R47; ADR 0008), kept in the browser and nowhere else
// (P4):
//
//   - the set, with its priority marks, in localStorage, so it's there next
//     time;
//   - talkgroups added for the session (a fireground link, R101) in
//     sessionStorage, so they last until the tab closes and are never saved.
//
// Hold (R44) is the player's and is never kept here.
import { entryOf, formatKept, keyOf, parseKept, resolve, type Entry, type Index, type Talkgroup } from './set.ts';

const SAVED = 'radioarchive.listening-set';
const SESSION = 'radioarchive.session-additions';

function read(storage: () => Storage, key: string): Entry[] {
	try {
		return parseKept(storage().getItem(key));
	} catch {
		// Storage switched off: the set lives as long as the page.
		return [];
	}
}

function write(storage: () => Storage, key: string, entries: Entry[]) {
	try {
		if (entries.length) storage().setItem(key, formatKept(entries));
		else storage().removeItem(key);
	} catch {
		// As above.
	}
}

const local = () => localStorage;
const session = () => sessionStorage;

export class ListeningState {
	/** The listening set, as saved. */
	saved: Entry[] = $state(read(local, SAVED));
	/** Talkgroups added for this session, apart from the set. */
	session: Entry[] = $state(read(session, SESSION));

	/** Told whenever the saved set changes: the account saves it too, when
	 * signed in (ADR 0009). The session's additions never tell it. */
	onsetchange: (() => void) | undefined;

	#saveSet() {
		write(local, SAVED, this.saved);
		this.onsetchange?.();
	}
	#saveSession() {
		write(session, SESSION, this.session);
	}

	/**
	 * Rewrites what's kept under each talkgroup's key as the catalogue has it
	 * now (a deployment that gained or lost a system), so a talkgroup is
	 * found by its key alone. What isn't public now is kept as it is: it may
	 * be public again later, and it's never shown or sent meanwhile.
	 */
	settle(ix: Index) {
		const fix = (es: Entry[]) => {
			const out: Entry[] = [];
			const seen = new Set<string>();
			for (const e of es) {
				const r = resolve(ix, [e])[0];
				const next = r ? entryOf(r.talkgroup, !!e.priority) : e;
				if (seen.has(keyOf(next))) continue;
				seen.add(keyOf(next));
				out.push(next);
			}
			return out;
		};
		const saved = fix(this.saved);
		const inSet = new Set(saved.map(keyOf));
		const added = fix(this.session).filter((e) => !inSet.has(keyOf(e)));
		if (JSON.stringify(saved) !== JSON.stringify(this.saved)) {
			this.saved = saved;
			this.#saveSet();
		}
		if (JSON.stringify(added) !== JSON.stringify(this.session)) {
			this.session = added;
			this.#saveSession();
		}
	}

	/** Whether a talkgroup is in the saved set. */
	inSet(t: Talkgroup): boolean {
		return this.saved.some((e) => keyOf(e) === keyOf(t));
	}

	/** Whether a talkgroup was added for this session only. */
	inSession(t: Talkgroup): boolean {
		return this.session.some((e) => keyOf(e) === keyOf(t));
	}

	/** Whether a talkgroup in the set is marked priority. */
	isPriority(t: Talkgroup): boolean {
		return this.saved.some((e) => keyOf(e) === keyOf(t) && e.priority);
	}

	/** Puts talkgroups in the set as they are now (a group's, R42), keeping
	 * any already there, and takes them out of the session's additions. */
	add(...ts: Talkgroup[]) {
		const fresh = ts.filter((t, i) => !this.inSet(t) && ts.findIndex((u) => keyOf(u) === keyOf(t)) === i);
		if (fresh.length) {
			this.saved = [...this.saved, ...fresh.map((t) => entryOf(t))];
			this.#saveSet();
		}
		const keys = new Set(ts.map(keyOf));
		if (this.session.some((e) => keys.has(keyOf(e)))) {
			this.session = this.session.filter((e) => !keys.has(keyOf(e)));
			this.#saveSession();
		}
	}

	/** Takes talkgroups out of the set. */
	remove(...ts: Talkgroup[]) {
		const keys = new Set(ts.map(keyOf));
		if (!this.saved.some((e) => keys.has(keyOf(e)))) return;
		this.saved = this.saved.filter((e) => !keys.has(keyOf(e)));
		this.#saveSet();
	}

	/** Marks a talkgroup in the set priority, or takes the mark off (R43). */
	setPriority(t: Talkgroup, priority: boolean) {
		this.saved = this.saved.map((e) => (keyOf(e) === keyOf(t) ? (priority ? { ...e, priority: true } : { system: e.system, id: e.id }) : e));
		this.#saveSet();
	}

	/**
	 * Adds a talkgroup for this session (R47): apart from the set, until the
	 * tab closes. A talkgroup already in the set is left as it is. Says
	 * whether it was in the set.
	 */
	addForSession(t: Talkgroup): 'in-set' | 'added' {
		if (this.inSet(t)) return 'in-set';
		if (!this.inSession(t)) {
			this.session = [...this.session, entryOf(t)];
			this.#saveSession();
		}
		return 'added';
	}

	/** Keeps a talkgroup added for the session: it joins the set (R47). */
	keep(t: Talkgroup) {
		this.add(t);
	}

	/** Replaces the set with another (a share link's, R41). The session's
	 * additions now in the set leave them. */
	replace(entries: Entry[]) {
		this.saved = entries.map((e) => (e.priority ? { system: e.system, id: e.id, priority: true } : { system: e.system, id: e.id }));
		this.#saveSet();
		const keys = new Set(this.saved.map(keyOf));
		if (this.session.some((e) => keys.has(keyOf(e)))) {
			this.session = this.session.filter((e) => !keys.has(keyOf(e)));
			this.#saveSession();
		}
	}
}

let shared: ListeningState | undefined;

/** The page's one listening state, shared by the Listen page and the
 * account (the frame's account menu). */
export function listeningState(): ListeningState {
	shared ??= new ListeningState();
	return shared;
}
