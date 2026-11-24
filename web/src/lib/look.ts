// R82: the listener's look (Harbour, the default, or Console) and theme
// (light, dark, or the system's), saved in the browser as cookies so the
// server sends the page already in them (web/web.go) and the default never
// flashes first. The cookie names are web.LookCookie and web.ThemeCookie.
//
// ArchiveUx draws them from the root element: data-look="console" for
// Console (none for Harbour), data-theme="light" or "dark" (none follows the
// system).

export type Look = 'harbour' | 'console';
export type Theme = 'system' | 'light' | 'dark';

export const looks: { value: Look; label: string }[] = [
	{ value: 'harbour', label: 'Harbour' },
	{ value: 'console', label: 'Console' }
];

export const themes: { value: Theme; label: string }[] = [
	{ value: 'system', label: 'System' },
	{ value: 'light', label: 'Light' },
	{ value: 'dark', label: 'Dark' }
];

/** The look and theme the page was sent in. */
export function current(): { look: Look; theme: Theme } {
	const d = document.documentElement.dataset;
	return {
		look: d.look === 'console' ? 'console' : 'harbour',
		theme: d.theme === 'light' || d.theme === 'dark' ? d.theme : 'system'
	};
}

// About 400 days, the most a browser keeps a cookie.
const keep = 400 * 24 * 60 * 60;

function save(name: string, value: string | undefined) {
	const secure = location.protocol === 'https:' ? '; Secure' : '';
	document.cookie =
		value === undefined
			? `${name}=; Path=/; Max-Age=0; SameSite=Lax${secure}`
			: `${name}=${value}; Path=/; Max-Age=${keep}; SameSite=Lax${secure}`;
}

/** Shows the look at once, and saves it for the next page sent. */
export function setLook(look: Look) {
	if (look === 'console') document.documentElement.dataset.look = look;
	else delete document.documentElement.dataset.look;
	save('look', look);
}

/** Shows the theme at once, and saves it; System forgets the choice. */
export function setTheme(theme: Theme) {
	if (theme === 'system') delete document.documentElement.dataset.theme;
	else document.documentElement.dataset.theme = theme;
	save('theme', theme === 'system' ? undefined : theme);
}
