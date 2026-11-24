// R82: the listener's look (Harbour, the default, or Console) and theme
// (light, dark, or the system's), chosen in ArchiveUx's account menu, which
// works signed out, and saved in the browser as cookies so the server sends
// the page already in them (web/web.go) and the default never flashes
// first. The cookie names are web.LookCookie and web.ThemeCookie.
//
// ArchiveUx draws them from the root element: data-look="console" for
// Console (none for Harbour), data-theme="light" or "dark" (none follows the
// system). Its look.js is the first-paint rule; standalone, RadioArchive
// names no shared cookie, so the look is the browser's own choice (R104's
// parent-domain cookie is M7's).
import { lookCookie, pickLook } from 'archiveux/look.js';

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

// About 400 days, the most a browser keeps a cookie.
const keep = 400 * 24 * 60 * 60;
const secure = () => location.protocol === 'https:';

/** Shows the look at once, and saves it for the next page sent. */
export function setLook(look: Look) {
	if (look === 'console') document.documentElement.dataset.look = look;
	else delete document.documentElement.dataset.look;
	document.cookie = lookCookie(look, { name: 'look', secure: secure() });
}

/** Shows the theme at once, and saves it; System forgets the choice. */
export function setTheme(theme: Theme) {
	if (theme === 'system') delete document.documentElement.dataset.theme;
	else document.documentElement.dataset.theme = theme;
	const flags = `Path=/; SameSite=Lax${secure() ? '; Secure' : ''}`;
	document.cookie = theme === 'system' ? `theme=; Max-Age=0; ${flags}` : `theme=${theme}; Max-Age=${keep}; ${flags}`;
}
