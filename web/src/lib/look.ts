// R82: the listener's look (Harbour, the default, or Console) and theme
// (light, dark, or the system's), chosen in ArchiveUx's account menu, which
// works signed out, and saved in the browser as cookies so the server sends
// the page already in them (web/web.go) and the default never flashes
// first. The cookie names are web.LookCookie and web.ThemeCookie.
//
// ArchiveUx draws them from the root element: data-look="console" for
// Console (none for Harbour), data-theme="light" or "dark" (none follows the
// system). Its look.js is the first-paint rule. Standalone, RadioArchive
// names no shared cookie, so the look is the browser's own choice, in a
// cookie of this host's own.
//
// Beside PagerArchive (R104; ADR 0054), with LOOK_COOKIE_DOMAIN set, the
// server marks the page data-look-shared="<domain>", and the look is the one
// `look` cookie both apps share on that domain, written as ArchiveUx's
// lookCookie writes it: the cookie's look wins over the account's, as in
// PagerArchive. The light or dark theme is shared the same way, in a
// `theme` cookie of its own (ArchiveUx 0.7.0, its rule 8).
import { applyTheme, lookCookie, pickLook, pickTheme } from 'archiveux/look.js';

export type Look = 'harbour' | 'console';
export type Theme = 'system' | 'light' | 'dark';

/** The look and theme the page was sent in. */
export function current(): { look: Look; theme: Theme } {
	const d = document.documentElement.dataset;
	return {
		look: pickLook({ own: d.look }) as Look,
		theme: d.theme === 'light' || d.theme === 'dark' ? d.theme : 'system'
	};
}

/** The parent domain the look is shared on, or undefined standalone. */
export function sharedDomain(): string | undefined {
	return document.documentElement.dataset.lookShared || undefined;
}

/**
 * The look a signed-in account's profile comes in as (R104): shared, the
 * shared cookie's when it holds one, so a look chosen in PagerArchive isn't
 * undone by the radio account's older one; else the account's.
 */
export function accountLook(own: Look | undefined, shared: boolean, cookies: string): Look {
	return pickLook({ cookies, cookie: shared ? 'look' : undefined, own }) as Look;
}

const secure = () => location.protocol === 'https:';

/** Shows the look at once, and saves it for the next page sent. */
export function setLook(look: Look) {
	if (look === 'console') document.documentElement.dataset.look = look;
	else delete document.documentElement.dataset.look;
	const domain = sharedDomain();
	// Shared, this host's own look cookie is ended first, so the shared one
	// is the only `look` the browser sends (web/combined.go).
	if (domain) document.cookie = 'look=; Path=/; Max-Age=0';
	document.cookie = lookCookie(look, { name: 'look', domain, secure: secure() });
}

/**
 * The theme a signed-in account's profile comes in as: shared, the shared
 * cookie's when it holds light or dark, else the account's, as accountLook.
 */
export function accountTheme(own: Theme | undefined, shared: boolean, cookies: string): Theme {
	return pickTheme({ cookies, cookie: shared ? 'theme' : undefined, own }) as Theme;
}

/**
 * Shows the theme at once, and saves it; System forgets the choice. Shared,
 * in the shared `theme` cookie, this host's own ended first (ArchiveUx's
 * applyTheme, 0.7.0).
 */
export function setTheme(theme: Theme) {
	applyTheme(theme, { name: 'theme', domain: sharedDomain(), secure: secure() });
}
