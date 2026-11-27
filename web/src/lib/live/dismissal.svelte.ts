// Whether the listener has put the recorder notice away (SPEC.md R15; ADR
// 0050): by its close button, or by letting it time out. Kept for the
// browser session, so it stays away on every page and through a reload,
// until the server says the outage is over. The next outage is new
// information, so it shows again.
//
// The feed carries no outage of its own, only on and off (ADR 0010), so
// "the same outage" is: no off seen since the dismissal. An off is the
// server's off, or a socket that is listening without the notice on
// (FeedHandlers.noticeOff), never a new socket's welcome, which turns
// every notice off only until the server says again.
const KEY = 'radioarchive.recorder-notice-dismissed';

function kept(): boolean {
	try {
		return sessionStorage.getItem(KEY) === '1';
	} catch {
		return false;
	}
}

export class NoticeDismissal {
	dismissed = $state(kept());

	/** The listener put the notice away. */
	dismiss() {
		this.dismissed = true;
		try {
			sessionStorage.setItem(KEY, '1');
		} catch {
			// Storage switched off: it stays away as long as the page.
		}
	}

	/** The server says the notice is off: the outage is over. */
	ended() {
		if (!this.dismissed) return;
		this.dismissed = false;
		try {
			sessionStorage.removeItem(KEY);
		} catch {
			// Nothing kept.
		}
	}
}
