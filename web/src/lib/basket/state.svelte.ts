// The listener's download basket in the browser (SPEC.md P4, R64, R110;
// ADR 0018): kept in localStorage, as the listening set is (ADR 0008), and
// saved to the listener's profile too when signed in (the account, ADR
// 0009, is told of every change). The server keeps nothing of it (P4).
// Another tab's changes arrive through the storage event, so every open
// page shows the same basket.
import {
	emptyBasket,
	parseBasket,
	withCall,
	withoutCall,
	withConversation,
	withConversationEdited,
	withoutConversation,
	withInConversation,
	withoutInConversation,
	withDownloaded,
	has,
	type Basket,
	type Scope
} from './basket.ts';

const KEY = 'radioarchive.basket';

function read(): Basket {
	try {
		const raw = localStorage.getItem(KEY);
		return raw ? parseBasket(JSON.parse(raw)) : emptyBasket();
	} catch {
		// Storage switched off, or not a basket: an empty one, for as long as the page.
		return emptyBasket();
	}
}

function write(b: Basket) {
	try {
		if (b.calls.length || b.conversations.length) localStorage.setItem(KEY, JSON.stringify({ v: 1, ...b }));
		else localStorage.removeItem(KEY);
	} catch {
		// As above.
	}
}

export class BasketState {
	basket: Basket = $state.raw(read());

	/** Told whenever the basket changes here: the account saves it too, when signed in. */
	onchange: (() => void) | undefined;

	constructor() {
		if (typeof window === 'undefined') return;
		window.addEventListener('storage', (e) => {
			if (e.key === KEY || e.key === null) this.basket = read();
		});
	}

	#set(b: Basket) {
		if (b === this.basket) return;
		this.basket = b;
		write(b);
		this.onchange?.();
	}

	has(id: string): boolean {
		return has(this.basket, id);
	}

	add(id: string) {
		this.#set(withCall(this.basket, id, Date.now()));
	}

	remove(id: string) {
		this.#set(withoutCall(this.basket, id));
	}

	/** A new conversation; its ID. */
	newConversation(id: string, name: string, note: string) {
		this.#set(withConversation(this.basket, id, name, note));
	}

	editConversation(id: string, name: string, note: string) {
		this.#set(withConversationEdited(this.basket, id, name, note));
	}

	deleteConversation(id: string) {
		this.#set(withoutConversation(this.basket, id));
	}

	putInConversation(convId: string, id: string, startOf?: (id: string) => string | undefined) {
		this.#set(withInConversation(this.basket, convId, id, Date.now(), startOf));
	}

	takeOutOfConversation(convId: string, id: string) {
		this.#set(withoutInConversation(this.basket, convId, id));
	}

	downloaded(s: Scope) {
		this.#set(withDownloaded(this.basket, s, Date.now()));
	}

	/** Empties the basket: its calls and its conversations (R64's Clear). */
	clear() {
		this.#set(emptyBasket());
	}

	/** Replaces the basket with the account's (ADR 0009's first sign-in rule). The account isn't told. */
	replace(b: Basket) {
		this.basket = parseBasket(b);
		write(this.basket);
	}
}

let shared: BasketState | undefined;

/** The page's one basket, shared by every page, the dock and the account. */
export function basketState(): BasketState {
	shared ??= new BasketState();
	return shared;
}
