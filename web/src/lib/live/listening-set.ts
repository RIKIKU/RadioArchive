// The one place the listener hands its listening set to the live feed
// (SPEC.md R36, R41, R47): the set plus the talkgroups added for the
// session, sent when the page has them and again whenever either changes.
// Priority marks and hold stay in the browser: the player orders its own
// queue (R43, R44).
//
// TODO(#56, #58): send this on the live WebSocket (/api/v1/live) in the
// message ADR 0007 defines, once #56 has merged and #58 opens the socket.
// Until then it only keeps the last set and announces it on the window as
// a "radioarchive:listening-set" event (the browser tests listen for it).
// Nothing else needs to change: the Listen page already calls this on
// every change.

/** A talkgroup the live feed is to send: its system only when there are several. */
export interface LiveTalkgroup {
	system?: number;
	id: number;
}

let last: LiveTalkgroup[] | undefined;

/** The set most recently handed to the live feed, if any. */
export const lastSent = (): LiveTalkgroup[] | undefined => last;

/** Hands the live feed the talkgroups to hear: the set plus the session's additions. */
export function sendListeningSet(talkgroups: LiveTalkgroup[]): void {
	const next = talkgroups.map((t) => (t.system === undefined ? { id: t.id } : { system: t.system, id: t.id }));
	if (last && JSON.stringify(last) === JSON.stringify(next)) return;
	last = next;
	window.dispatchEvent(new CustomEvent('radioarchive:listening-set', { detail: next }));
}
