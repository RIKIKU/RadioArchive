// The Accounts page's accounts (GET /api/v1/admin/accounts; ADR 0025,
// 0033): what an account is, the name it goes by, the search, and its
// status in words.

/** An account, as the admin API lists it. */
export interface Account {
	/** Authentik's user ID (the OIDC subject). */
	id: string;
	name: string;
	email: string;
	role: 'admin' | 'listener';
	status: 'active' | 'disabled';
	first_signed_in: string;
	last_signed_in: string;
	/** The status was changed here and hasn't reached Authentik yet. */
	authentik_pending: boolean;
}

/** The name an account goes by on the page: its name, else its email, else its ID. */
export const accountLabel = (a: Account): string => a.name.trim() || a.email || `Account ${a.id}`;

/** Whether the search matches the account's name or email, ignoring case and spaces at the ends. */
export function matches(a: Account, query: string): boolean {
	const q = query.trim().toLocaleLowerCase('en-AU');
	return q === '' || a.name.toLocaleLowerCase('en-AU').includes(q) || a.email.toLocaleLowerCase('en-AU').includes(q);
}

/** An account's status, as a StateBadge shows it. */
export function statusOf(a: Account): { state: 'ok' | 'warning' | 'error' | 'neutral'; words: string } {
	if (a.authentik_pending) return { state: 'warning', words: a.status === 'disabled' ? 'Disabled here, not yet in Authentik' : 'Enabled here, not yet in Authentik' };
	return a.status === 'disabled' ? { state: 'neutral', words: 'Disabled' } : { state: 'ok', words: 'Active' };
}
