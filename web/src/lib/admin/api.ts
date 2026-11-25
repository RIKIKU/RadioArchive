// The admin pages' requests to the admin API (/api/v1/admin, ADR 0003,
// 0025). The API is the guard: every request is checked there, and a page
// only follows its answer (ADR 0027). Signed out is 401, signed in but not
// an admin is 403, and a check that couldn't be made is 503.

export const ADMIN_API = '/api/v1/admin';

/** What a page may show, from the admin API's answer. */
export type Access =
	/** Asking the admin API. */
	| 'checking'
	/** An admin: the page shows. */
	| 'open'
	/** Nobody signed in: the page offers sign-in. */
	| 'sign-in'
	/** Signed in, not an admin. */
	| 'not-admin'
	/** The API couldn't be reached, or couldn't check the account. */
	| 'unavailable';

export function accessOf(status: number): Access {
	if (status >= 200 && status < 300) return 'open';
	if (status === 401) return 'sign-in';
	if (status === 403) return 'not-admin';
	return 'unavailable';
}

/** An admin API answer: its status, and its body when it was JSON. */
export interface Answer<T> {
	status: number;
	access: Access;
	body?: T;
}

/** An error answer's body (R78). */
export interface AdminError {
	error: string;
	message: string;
}

/** Calls the admin API; a network failure is a status of 0. */
export async function admin<T>(method: string, path: string, body?: unknown): Promise<Answer<T>> {
	let r: Response;
	try {
		r = await fetch(ADMIN_API + path, {
			method,
			headers: body === undefined ? { Accept: 'application/json' } : { Accept: 'application/json', 'Content-Type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
	} catch {
		return { status: 0, access: 'unavailable' };
	}
	let parsed: T | undefined;
	try {
		parsed = (await r.json()) as T;
	} catch {
		parsed = undefined;
	}
	return { status: r.status, access: accessOf(r.status), body: parsed };
}

/** GET /api/v1/admin/retention (R88; ADR 0005): the period and the recordings volume. */
export interface Retention {
	days: number;
	default_days: number;
	min_days: number;
	max_days: number;
	next_run: string;
	disk: Disk | null;
	last_nightly: { finished_at: string; ok: boolean } | null;
}

/** The recordings volume's state; message says it in words when it isn't ok. */
export interface Disk {
	free_bytes: number;
	total_bytes: number;
	free_percent: number;
	min_free_percent: number;
	warning_free_percent: number;
	state: 'ok' | 'warning' | 'low' | 'full';
	message?: string;
}

/** GET /api/v1/admin/backup (R89; ADR 0041): the nightly backup's state. */
export interface Backup {
	state: 'ok' | 'failing' | 'pending' | 'off';
	last_run_at: string | null;
	last_run_ok: boolean;
	running: boolean;
	last_good_at: string | null;
	last_good_night?: string;
	last_good_bytes: number;
	nights: number;
	/** Why backups are failing, in words, when they are. */
	message?: string;
}

/** The backup alert admins see on every admin page (R89), or nothing. */
export const backupAlert = (b: Backup | null | undefined): string | undefined =>
	b && b.state === 'failing' && b.message ? b.message : undefined;

/** GET /api/v1/admin/upgrade (R87, R89; ADR 0044): the automatic upgrade's state. */
export interface Upgrade {
	state: 'off' | 'ok' | 'running' | 'failing';
	last_check_at: string | null;
	last_result?: string;
	running_version?: string;
	latest_version?: string;
	last_upgrade_at: string | null;
	/** What failed, in words, when it did. */
	message?: string;
}

/** The upgrade alert admins see on every admin page (R89), or nothing. */
export const upgradeAlert = (u: Upgrade | null | undefined): string | undefined =>
	u && u.state === 'failing' && u.message ? u.message : undefined;

/** The disk warning admins see on every admin page (R88), or nothing. */
export const diskWarning = (d: Disk | null | undefined): string | undefined => (d && d.state !== 'ok' && d.message ? d.message : undefined);

const plain = new Map<string, Intl.DateTimeFormat>();

/** A moment on the deployment's clock: "Wed 25 Nov, 14:03", the year when it isn't this one. */
export function whenOf(iso: string, timeZone: string, now = new Date()): string {
	const d = new Date(iso);
	const yearOf = (x: Date) => new Intl.DateTimeFormat('en-AU', { timeZone, year: 'numeric' }).format(x);
	const withYear = yearOf(d) !== yearOf(now);
	const key = `${timeZone}|${withYear}`;
	let f = plain.get(key);
	if (!f) {
		f = new Intl.DateTimeFormat('en-AU', {
			timeZone,
			weekday: 'short',
			day: 'numeric',
			month: 'short',
			...(withYear ? { year: 'numeric' } : {}),
			hour: '2-digit',
			minute: '2-digit',
			hourCycle: 'h23'
		});
		plain.set(key, f);
	}
	return f.format(d);
}

/** How long ago, in words: "just now", "4 min ago", "3 h ago", "2 days ago". */
export function agoOf(iso: string, now = Date.now()): string {
	const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
	if (s < 60) return 'just now';
	const m = Math.floor(s / 60);
	if (m < 60) return `${m} min ago`;
	const h = Math.floor(m / 60);
	if (h < 48) return `${h} h ago`;
	return `${Math.floor(h / 24)} days ago`;
}

/** The deployment's time zone, as the catalogue gives it (section 13); UTC until it's known. */
export async function deploymentZone(): Promise<string> {
	try {
		const r = await fetch('/api/v1/catalogue', { headers: { Accept: 'application/json' } });
		if (!r.ok) return 'UTC';
		const c = (await r.json()) as { time_zone?: string };
		return c.time_zone || 'UTC';
	} catch {
		return 'UTC';
	}
}
