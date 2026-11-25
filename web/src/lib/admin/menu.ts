// The Admin section of the menu (SPEC.md R84; ADR 0027): a group of the
// side menu on desktops, and on phones a group of the bottom bar's More
// sheet. The frame shows it only while GET /api/v1/account says the account
// is an admin; it opens nothing by itself, since the admin API checks every
// request (ADR 0025).
//
// Each admin page adds its own entry here, in R84's order (Talkgroups,
// Units, Systems, Recorders, Accounts, Audit log, Settings), when it's
// built. A page that isn't built yet has no entry, so the menu never
// offers a page that isn't there. The group's heading links to the first.
import { adminIcons } from './icons.ts';

/** A page in the Admin section (ArchiveUx's menu Entry). */
export interface AdminEntry {
	href: string;
	label: string;
	icon?: string;
	svg?: string;
	/** What it's for, in a few words. */
	text: string;
}

/** The admin pages, in R84's order. */
export const adminPages: AdminEntry[] = [
	{ href: '/admin/recorders', label: 'Recorders', svg: adminIcons.recorders, text: 'Recorders, their signals and overlapping calls' },
	{ href: '/admin/audit', label: 'Audit log', svg: adminIcons.audit, text: 'Every admin change: who, how, when and what' },
	{ href: '/admin/settings', label: 'Settings', svg: adminIcons.settings, text: 'Retention and the recordings volume' }
];

/** The Admin group, for ArchiveUx's Menu and BottomBar. */
export function adminGroup(pages: AdminEntry[] = adminPages) {
	return {
		id: 'admin',
		label: 'Admin',
		icon: 'shield',
		text: 'Admin',
		href: pages[0].href,
		entries: pages
	};
}
