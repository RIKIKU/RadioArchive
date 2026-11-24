// What a signed-in listener's account holds (SPEC.md R41, R48, R110; ADR
// 0009): the listening set with its priority marks, the playback settings
// and the look. The set is in the browser's own format (ADR 0008). Hold and
// talkgroups added for the session are never in it. Nothing here touches
// the page or the network, so it can be tested on its own
// (tests/account-profile.spec.ts).
import { keyOf, type Entry } from '../listening/set.ts';
import type { Look, Theme } from '../look.ts';

export interface Profile {
	v: 1;
	talkgroups: Entry[];
	look?: Look;
	theme?: Theme;
	alerts: boolean;
}

/** GET /api/v1/account. */
export interface AccountState {
	/** Whether listeners can sign in here. */
	signin: boolean;
	account: { id: string; name?: string; email?: string } | null;
	profile: Profile | null;
	/** Whether the account can hold settings (an administrator's can't). */
	saves: boolean;
}

/** The browser's own settings, as a profile. */
export function profileOf(set: Entry[], look: Look, theme: Theme, alerts: boolean): Profile {
	return {
		v: 1,
		talkgroups: set.map((e) => (e.priority ? { system: e.system, id: e.id, priority: true } : { system: e.system, id: e.id })),
		look,
		theme,
		alerts
	};
}

/** Whether a profile holds no listening set: the browser's is saved to it on first sign-in (R41). */
export const holdsNoSet = (p: Profile | null): boolean => !p || p.talkgroups.length === 0;

const marks = (es: Entry[]) =>
	es
		.map((e) => `${keyOf(e)}${e.priority ? 'p' : ''}`)
		.sort()
		.join(',');

/** Whether two profiles hold the same: the same talkgroups and marks, in any order, and the same settings. */
export function sameProfile(a: Profile, b: Profile): boolean {
	return (
		marks(a.talkgroups) === marks(b.talkgroups) &&
		(a.look ?? 'harbour') === (b.look ?? 'harbour') &&
		(a.theme ?? 'system') === (b.theme ?? 'system') &&
		!!a.alerts === !!b.alerts
	);
}

/**
 * What a browser does when it finds its listener signed in (R41):
 *
 * - `adopt`: this browser was matched with this account before, so the
 *   account's settings are this browser's (when it holds any);
 * - `save`: the first sign-in here, and the account holds no set: the
 *   browser's is saved to it;
 * - `match`: the first sign-in here, and the account holds what the browser
 *   does: nothing to ask;
 * - `ask`: the first sign-in here, and the account holds another set: one
 *   prompt offers to replace the browser's with it. Nothing is merged.
 */
export function firstStep(linked: boolean, account: Profile | null, browser: Profile): 'adopt' | 'save' | 'match' | 'ask' {
	if (linked) return 'adopt';
	if (holdsNoSet(account)) return 'save';
	return sameProfile(account!, browser) ? 'match' : 'ask';
}
