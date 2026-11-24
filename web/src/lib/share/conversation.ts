// A shared conversation's link (SPEC.md P4, R66; ADR 0015, ADR 0020):
// /conversation#<packed>, the conversation's name, note and call IDs in
// order, packed as a zip's list is (one conversation, no loose calls, part
// 0). The list is in the fragment, which the browser never sends, so the
// server never sees the name, the note or the calls: it serves the app's
// own page for /conversation, and the page asks the call details for the
// calls. Nothing here touches the page or the network
// (tests/conversation-rules.spec.ts).
import { MAX_ZIP_URL, base64url, pack, unpack } from '../basket/basket.ts';

export const CONVERSATION_PATH = '/conversation';

/**
 * The longest link Share makes, path and fragment: ADR 0015's URL budget,
 * so a conversation's link is no longer than its zip's.
 */
export const MAX_LINK = MAX_ZIP_URL;

/** What a shared conversation's link carries. */
export interface SharedConversation {
	name: string;
	note: string;
	/** Call IDs, in the conversation's order. */
	calls: string[];
}

/** The link's path and fragment, however long. */
export const conversationPath = (c: SharedConversation): string =>
	`${CONVERSATION_PATH}#${base64url(pack({ calls: [], conversations: [{ name: c.name, note: c.note, calls: c.calls }] }))}`;

/** What Share gives for a conversation: a link, or why there's none. */
export type Share = { path: string; shortened: boolean } | { path?: undefined; problem: 'empty' | 'too-many' };

/**
 * The link Share copies for a conversation (ADR 0020): whole when it fits
 * in MAX_LINK; else with the note cut short, at a character, ending "…";
 * else, when the calls and name alone don't fit (over 325 calls or so),
 * none.
 */
export function shareOf(c: SharedConversation): Share {
	if (!c.calls.length) return { problem: 'empty' };
	const whole = conversationPath(c);
	if (whole.length <= MAX_LINK) return { path: whole, shortened: false };
	const chars = [...c.note];
	const cut = (n: number) => conversationPath({ ...c, note: n ? chars.slice(0, n).join('').trimEnd() + '…' : '' });
	if (cut(0).length > MAX_LINK) return { problem: 'too-many' };
	// The longest cut that fits: the path grows with the note.
	let lo = 0;
	let hi = chars.length - 1;
	while (lo < hi) {
		const mid = Math.ceil((lo + hi) / 2);
		if (cut(mid).length <= MAX_LINK) lo = mid;
		else hi = mid - 1;
	}
	return { path: cut(lo), shortened: true };
}

/**
 * A link's conversation, from its fragment (with or without the "#"), or
 * the whole link.
 * Undefined unless it's exactly what Share writes: one conversation, no
 * loose calls, not a part. A link past MAX_LINK that's otherwise one is
 * read: the server never sees it, so nothing refuses it.
 */
export function readConversation(fragment: string): SharedConversation | undefined {
	const got = unpack(fragment.slice(fragment.indexOf('#') + 1));
	if (!got || got.part || got.parts || got.list.calls.length || got.list.conversations.length !== 1) return undefined;
	const [v] = got.list.conversations;
	if (!v.name.trim()) return undefined;
	return { name: v.name, note: v.note, calls: v.calls };
}
