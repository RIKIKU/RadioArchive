// What a signed-in listener's account holds (SPEC.md R41, R48, R64, R110;
// ADR 0009, ADR 0018): the listening set with its priority marks, the
// playback settings, the look and the download basket with its
// conversations. The set and the basket are in the browser's own formats
// (ADR 0008, ADR 0018) and go together, as one profile. Hold and
// talkgroups added for the session are never in it. Nothing here touches
// the page or the network, so it can be tested on its own
// (tests/account-profile.spec.ts).
import { keyOf, type Entry } from '../listening/set.ts';
import type { Look, Theme } from '../look.ts';
import { emptyBasket, isEmpty, parseBasket, sameBasket, type Basket } from '../basket/basket.ts';

export interface Profile {
	v: 1;
	talkgroups: Entry[];
	look?: Look;
	theme?: Theme;
	alerts: boolean;
	/** A profile saved before the basket joined it (M3) has none. */
	basket?: Basket;
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
export function profileOf(set: Entry[], look: Look, theme: Theme, alerts: boolean, basket: Basket = emptyBasket()): Profile {
	return {
		v: 1,
		talkgroups: set.map((e) => (e.priority ? { system: e.system, id: e.id, priority: true } : { system: e.system, id: e.id })),
		look,
		theme,
		alerts,
		basket: parseBasket(basket)
	};
}

/**
 * Whether a profile holds nothing a listener collected: no listening set
 * and no basket. The browser's are saved to it on first sign-in (R41).
 */
export const holdsNoSet = (p: Profile | null): boolean => !p || (p.talkgroups.length === 0 && isEmpty(parseBasket(p.basket)));

const marks = (es: Entry[]) =>
	es
		.map((e) => `${keyOf(e)}${e.priority ? 'p' : ''}`)
		.sort()
		.join(',');

/** Whether two profiles hold the same: the same talkgroups and marks, in any order, the same settings and the same basket. */
export function sameProfile(a: Profile, b: Profile): boolean {
	return (
		marks(a.talkgroups) === marks(b.talkgroups) &&
		(a.look ?? 'harbour') === (b.look ?? 'harbour') &&
		(a.theme ?? 'system') === (b.theme ?? 'system') &&
		!!a.alerts === !!b.alerts &&
		sameBasket(a.basket ?? emptyBasket(), b.basket ?? emptyBasket())
	);
}

/**
 * What a browser does when it finds its listener signed in (R41):
 *
 * - `adopt`: this browser was matched with this account before, so the
 *   account's settings are this browser's (when it holds any);
 * - `save`: the first sign-in here, and the account holds no set and no
 *   basket: the browser's are saved to it;
 * - `match`: the first sign-in here, and the account holds what the browser
 *   does: nothing to ask;
 * - `ask`: the first sign-in here, and the account holds something else:
 *   one prompt offers to replace the browser's with it. Nothing is merged.
 */
export function firstStep(linked: boolean, account: Profile | null, browser: Profile): 'adopt' | 'save' | 'match' | 'ask' {
	if (linked) return 'adopt';
	if (holdsNoSet(account)) return 'save';
	return sameProfile(account!, browser) ? 'match' : 'ask';
}
