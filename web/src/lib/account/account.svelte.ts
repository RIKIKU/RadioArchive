// The listener's account in the browser (SPEC.md R41, R48, R82, R110; ADR
// 0009). Signing in only saves preferences: nothing here decides what the
// listener hears, and listening never waits for it (P3).
//
// - Signed out: nothing is sent anywhere; the browser keeps its own set.
// - The first sign-in in a browser: an account with no set gets the
//   browser's; one with another set brings one prompt offering to replace
//   the browser's with it, or keep the browser's (which then replaces the
//   account's). Nothing is merged. The browser remembers which account it
//   was matched with, by the account's ID only.
// - Signed in and matched: the account's settings are the browser's when
//   the page opens, and every change to the set, the marks, the playback
//   settings or the look is saved to the account too.
// - Hold and talkgroups added for the session are never saved.
// - The download basket goes with the set (ADR 0018): saved, adopted,
//   offered and cleared with it, never merged.
import type { ListeningState } from '../listening/state.svelte.ts';
import type { BasketState } from '../basket/state.svelte.ts';
import type { Entry } from '../listening/set.ts';
import type { Look, Theme } from '../look.ts';
import { firstStep, profileOf, type AccountState, type Profile } from './profile.ts';

const LINKED = 'radioarchive.account';

function linkedTo(): string | null {
	try {
		return localStorage.getItem(LINKED);
	} catch {
		return null;
	}
}

function link(id: string | null) {
	try {
		if (id) localStorage.setItem(LINKED, id);
		else localStorage.removeItem(LINKED);
	} catch {
		// Storage switched off: the next page asks again.
	}
}

/** What the account gets from the page around it. */
export interface Settings {
	listening: ListeningState;
	basket: BasketState;
	look(): Look;
	theme(): Theme;
	alerts(): boolean;
	/** Shows and keeps the account's look, theme and playback settings. */
	apply(p: Profile): void;
}

export type Mode =
	/** Reading the account. */
	| 'loading'
	| 'signed-out'
	/** Signed in, and the browser's settings are saved to the account. */
	| 'saving'
	/** Signed in; waiting for the listener's answer to the prompt. */
	| 'asking'
	/** Signed in to an account that holds no settings (an administrator's). */
	| 'not-saved'
	/** Couldn't reach the account. Listening goes on. */
	| 'unavailable';

export class Account {
	mode: Mode = $state('loading');
	signin = $state(true);
	who: AccountState['account'] = $state(null);
	/** The account's set, while the prompt asks about it. */
	offer: Profile | null = $state(null);
	/** A save that didn't go through, for the account menu to say. */
	saveFailed = $state(false);
	/** The clear's confirmation and outcome. */
	clearing: '' | 'confirm' | 'cleared' | 'failed' = $state('');

	#s: Settings;
	#timer: ReturnType<typeof setTimeout> | undefined;
	#quiet = false;

	constructor(s: Settings) {
		this.#s = s;
		s.listening.onsetchange = () => this.changed();
		s.basket.onchange = () => this.changed();
	}

	#browser(): Profile {
		return profileOf(this.#s.listening.saved, this.#s.look(), this.#s.theme(), this.#s.alerts(), this.#s.basket.basket);
	}

	/** Reads the account, and matches the browser with it (R41). */
	async load() {
		let st: AccountState;
		try {
			const r = await fetch('/api/v1/account', { headers: { Accept: 'application/json' } });
			if (!r.ok) throw new Error(String(r.status));
			st = (await r.json()) as AccountState;
		} catch {
			this.mode = 'unavailable';
			return;
		}
		this.signin = st.signin;
		this.who = st.account;
		if (!st.account) {
			// Signed out (or the session ended): the next sign-in here asks again.
			link(null);
			this.mode = 'signed-out';
			return;
		}
		if (!st.saves) {
			this.mode = 'not-saved';
			return;
		}
		const step = firstStep(linkedTo() === st.account.id, st.profile, this.#browser());
		if (step === 'ask') {
			this.offer = st.profile;
			this.mode = 'asking';
			return;
		}
		link(st.account.id);
		this.mode = 'saving';
		if (step === 'adopt' && st.profile) this.#adopt(st.profile);
		if (step === 'save') await this.save();
	}

	#adopt(p: Profile) {
		this.#quiet = true;
		try {
			this.#s.listening.replace(p.talkgroups as Entry[]);
			// A profile from before the basket joined it keeps this
			// browser's, which is saved to it at the next change.
			if (p.basket) this.#s.basket.replace(p.basket);
			this.#s.apply(p);
		} finally {
			this.#quiet = false;
		}
	}

	/** The prompt's answer: the account's set replaces the browser's. */
	useAccounts() {
		if (!this.offer || !this.who) return;
		this.#adopt(this.offer);
		this.#settle();
	}

	/** The prompt's answer: the browser's set replaces the account's. */
	async keepBrowsers() {
		if (!this.offer || !this.who) return;
		this.#settle();
		await this.save();
	}

	#settle() {
		link(this.who!.id);
		this.offer = null;
		this.mode = 'saving';
	}

	/** Something saved changed: saved to the account too, a moment later. */
	changed() {
		if (this.#quiet || this.mode !== 'saving') return;
		clearTimeout(this.#timer);
		this.#timer = setTimeout(() => void this.save(), 400);
	}

	async save() {
		clearTimeout(this.#timer);
		if (this.mode !== 'saving') return;
		try {
			const r = await fetch('/api/v1/account/profile', {
				method: 'PUT',
				headers: { 'Content-Type': 'application/json' },
				body: JSON.stringify(this.#browser())
			});
			if (r.status === 401) {
				this.who = null;
				this.mode = 'signed-out';
				link(null);
				return;
			}
			this.saveFailed = !r.ok;
		} catch {
			this.saveFailed = true;
		}
	}

	/** Empties the account (R110). This browser keeps its own settings. */
	async clear() {
		try {
			const r = await fetch('/api/v1/account/profile', { method: 'DELETE' });
			this.clearing = r.ok ? 'cleared' : 'failed';
		} catch {
			this.clearing = 'failed';
		}
	}

	/** Signs out here and at Authentik. The browser keeps its set. */
	async signOut() {
		clearTimeout(this.#timer);
		let to = '/?signed_out=1';
		try {
			const r = await fetch('/auth/signout', { method: 'POST' });
			if (r.ok) to = ((await r.json()) as { redirect: string }).redirect;
		} catch {
			// The server's sign-out page says what happened.
		}
		link(null);
		location.assign(to);
	}
}
