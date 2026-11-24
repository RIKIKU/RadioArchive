// The one place the listener hands its listening set to the live feed
// (SPEC.md R36, R41, R47): the set plus the talkgroups added for the
// session, sent when the page has them and again whenever either changes.
// Priority marks and hold stay in the browser: the player orders its own
// queue (R43, R44).
//
// The Listen page calls sendListeningSet on every change. Once the listener
// presses Start listening, the player attaches its live feed (feed.ts), which
// sends the set as ADR 0007's `set` message, and again on every change. The
// set is also announced on the window as a "radioarchive:listening-set"
// event (the browser tests listen for it).

/** A talkgroup the live feed is to send: its system only when there are several. */
export interface LiveTalkgroup {
	system?: number;
	id: number;
}

/** What hears the set: the live feed, once listening has started. */
export interface SetListener {
	setTalkgroups(set: LiveTalkgroup[]): void;
}

let last: LiveTalkgroup[] | undefined;
let listener: SetListener | undefined;

/** The set most recently handed to the live feed, if any. */
export const lastSent = (): LiveTalkgroup[] | undefined => last;

/** Hands the live feed the talkgroups to hear: the set plus the session's additions. */
export function sendListeningSet(talkgroups: LiveTalkgroup[]): void {
	const next = talkgroups.map((t) => (t.system === undefined ? { id: t.id } : { system: t.system, id: t.id }));
	if (last && JSON.stringify(last) === JSON.stringify(next)) return;
	last = next;
	listener?.setTalkgroups(next);
	window.dispatchEvent(new CustomEvent('radioarchive:listening-set', { detail: next }));
}

/**
 * Attaches the live feed, which hears every set from now on, starting with
 * the last one; undefined detaches it. Returns the set to start from.
 */
export function attachFeed(l: SetListener | undefined): LiveTalkgroup[] {
	listener = l;
	return last ?? [];
}
