/// <reference types="node" />
// Listeners' sign-in and the saved profile (SPEC.md P3, R41, R48, R103,
// R110, R111, A33; ADR 0009), in a real browser against the compose stack
// (scripts/ci/e2e-signin.sh): Authentik's own pages are used as a listener
// would, sign-up codes are read from Mailpit, and every account is made up
// and new for its test.
import { test, expect, type APIRequestContext, type Browser, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

/** A36 for what sign-in adds to the page. */
async function accessible(page: Page) {
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
}

const MAILPIT = process.env.RA_MAILPIT_URL ?? 'http://localhost:8025';

type Entry = { system?: number; id: number; priority?: boolean };
type Account = { signin: boolean; account: { id: string; name?: string; email?: string; admin: boolean } | null; profile: { talkgroups: Entry[]; look?: string } | null; saves: boolean };

/** A new made-up address for each account. */
const address = (who: string) => `${who}-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 7)}@example.test`;
const passwordOf = (email: string) => `correct horse ${email.split('@')[0]} staple`;

/**
 * The text of the newest email to address after since. Authentik's worker
 * sends it from its queue, so it can take a while.
 */
async function mail(request: APIRequestContext, to: string, since: Date): Promise<string> {
	const deadline = Date.now() + 60_000;
	let seen: { ID: string; Created: string; Subject: string }[] = [];
	while (Date.now() < deadline) {
		const res = await request.get(`${MAILPIT}/api/v1/search`, { params: { query: `to:"${to}"` } });
		seen = ((await res.json()) as { messages: typeof seen }).messages;
		const m = seen.find((m) => new Date(m.Created) > since);
		if (m) return ((await (await request.get(`${MAILPIT}/api/v1/message/${m.ID}`)).json()) as { Text: string }).Text;
		await new Promise((r) => setTimeout(r, 500));
	}
	throw new Error(`no email to ${to} within 60 s; the catcher had ${seen.length} for that address`);
}

/**
 * Answers one of Authentik's flow pages. The page redraws while its loading
 * overlay is up, which empties a field filled in before that.
 */
async function answer(page: Page, fields: Record<string, string>) {
	const first = page.locator(`input[name="${Object.keys(fields)[0]}"]`);
	await expect(first).toBeVisible({ timeout: 60_000 });
	await expect(page.locator('ak-loading-overlay')).toHaveCount(0, { timeout: 30_000 });
	for (const [name, value] of Object.entries(fields)) await page.locator(`input[name="${name}"]`).fill(value);
	await page.locator('button[type="submit"]').click();
}

/** A new browser whose listening set is set. */
async function browserWith(browser: Browser, set: Entry[], width = 1280): Promise<Page> {
	const context = await browser.newContext({ viewport: { width, height: width < 600 ? 800 : 900 } });
	await context.addInitScript((kept) => {
		if (!sessionStorage.getItem('test.seeded')) {
			localStorage.setItem('radioarchive.listening-set', kept);
			sessionStorage.setItem('test.seeded', '1');
		}
	}, JSON.stringify({ v: 1, talkgroups: set }));
	const page = await context.newPage();
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 2, name: 'Your listening set' })).toBeVisible();
	return page;
}

async function openMenu(page: Page) {
	const button = page.getByRole('button', { name: /^Account menu/ });
	if ((await button.getAttribute('aria-expanded')) !== 'true') await button.click();
}

async function closeMenu(page: Page) {
	const button = page.getByRole('button', { name: /^Account menu/ });
	if ((await button.getAttribute('aria-expanded')) === 'true') await button.click();
}

/** Back in the radio, signed in. */
async function signedIn(page: Page, baseURL: string) {
	await page.waitForURL((u) => u.origin === new URL(baseURL).origin && !u.pathname.startsWith('/auth/'), { timeout: 60_000 });
	await expect(page.getByRole('button', { name: /^Account menu, / })).not.toHaveAccessibleName('Account menu, signed out', { timeout: 30_000 });
}

/** Signs up from the account menu (R103): details, then the emailed code. */
async function signUp(page: Page, request: APIRequestContext, baseURL: string, email: string) {
	await openMenu(page);
	await page.getByRole('link', { name: 'Sign in' }).click();
	await page.getByRole('link', { name: /Sign up/ }).click();
	const since = new Date(Date.now() - 2000);
	await answer(page, { email, name: 'Made Up Listener', password: passwordOf(email), password_repeat: passwordOf(email) });
	const code = /\b\d{6}\b/.exec(await mail(request, email, since))?.[0];
	expect(code, 'a code in the email').toBeTruthy();
	await answer(page, { code: code! });
	await signedIn(page, baseURL);
}

/** Signs in from the account menu with an account made before. */
async function signIn(page: Page, baseURL: string, email: string) {
	await openMenu(page);
	await page.getByRole('link', { name: 'Sign in' }).click();
	await answer(page, { uidField: email });
	await answer(page, { password: passwordOf(email) });
	await signedIn(page, baseURL);
}

async function account(page: Page): Promise<Account> {
	const r = await page.request.get('/api/v1/account');
	expect(r.status()).toBe(200);
	return (await r.json()) as Account;
}

const savedSet = (page: Page) => page.evaluate(() => JSON.parse(localStorage.getItem('radioarchive.listening-set') ?? '{"talkgroups":[]}').talkgroups);
const ids = (es: Entry[]) => es.map((e) => e.id).sort();

/** The pull request's screenshots (RA_SHOTS names a folder). */
async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS) return;
	await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}.png` });
}

test('A33: a signed-out listener with {1001} signs up; the set is saved to the new account, with no prompt', async ({ browser, request, baseURL }) => {
	const page = await browserWith(browser, [{ id: 1001 }]);
	expect((await account(page)).account).toBeNull();
	const email = address('first');
	await signUp(page, request, baseURL!, email);

	await expect(page.getByRole('heading', { name: 'Use the listening set saved to your account?' })).toHaveCount(0);
	await expect.poll(async () => (await account(page)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1001 }]);
	const st = await account(page);
	expect(st.account?.email).toBe(email);
	// Listening is as it was: the set, and Start listening.
	expect(ids(await savedSet(page))).toEqual([1001]);
	await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
});

test('A33: an account with {1002} on a browser with {1001}: one prompt, and nothing is merged either way', async ({ browser, request, baseURL }) => {
	// The account, saved with {1002} from another browser.
	const email = address('second');
	const other = await browserWith(browser, [{ id: 1002 }]);
	await signUp(other, request, baseURL!, email);
	await expect.poll(async () => (await account(other)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1002 }]);
	await other.context().close();

	// Replace: this browser takes the account's set, and only that.
	const page = await browserWith(browser, [{ id: 1001 }]);
	await signIn(page, baseURL!, email);
	const prompt = page.getByRole('region', { name: 'Use the listening set saved to your account?' });
	await expect(prompt).toBeVisible();
	await expect(prompt.getByRole('listitem')).toHaveText(['Fireground 41']);
	await accessible(page);
	await prompt.getByRole('button', { name: "Use my account's set" }).click();
	await expect(prompt).toHaveCount(0);
	expect(ids(await savedSet(page))).toEqual([1002]);
	await page.getByText(/^Talkgroups on \(/).click();
	await expect(page.getByRole('list', { name: 'Your listening set' }).getByRole('listitem')).toHaveCount(1);
	expect(ids((await account(page)).profile!.talkgroups)).toEqual([1002]);
	// Once: the next page doesn't ask again.
	await page.reload();
	await expect(page.getByRole('heading', { level: 2, name: 'Your listening set' })).toBeVisible();
	await page.waitForTimeout(1000);
	await expect(prompt).toHaveCount(0);
	expect(ids(await savedSet(page))).toEqual([1002]);

	// Keep: a third browser keeps its own, which replaces the account's.
	const third = await browserWith(browser, [{ id: 1001, priority: true }]);
	await signIn(third, baseURL!, email);
	const asked = third.getByRole('region', { name: 'Use the listening set saved to your account?' });
	await expect(asked).toBeVisible();
	await asked.getByRole('button', { name: "Keep this browser's set" }).click();
	await expect.poll(async () => (await account(third)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1001, priority: true }]);
	expect(await savedSet(third)).toEqual([{ id: 1001, priority: true }]);
});

test('signed in, changes are saved to the account; hold and session additions never are', async ({ browser, request, baseURL }) => {
	const page = await browserWith(browser, [{ id: 1001 }]);
	await signUp(page, request, baseURL!, address('changes'));
	await expect.poll(async () => (await account(page)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1001 }]);

	// A priority mark and the look are saved.
	await page.getByRole('checkbox', { name: 'Priority: Dispatch North' }).check();
	await openMenu(page);
	await page.getByRole('combobox', { name: 'Look' }).selectOption('console');
	await closeMenu(page);
	await expect.poll(async () => (await account(page)).profile, { timeout: 15_000 }).toMatchObject({ talkgroups: [{ id: 1001, priority: true }], look: 'console' });

	// A fireground link's talkgroup is added for the session only (R47).
	await page.goto('/?fireground=41');
	await expect(page.getByRole('list', { name: 'Added for this session' })).toContainText('Fireground 41');
	await page.waitForTimeout(1500);
	expect(ids((await account(page)).profile!.talkgroups)).toEqual([1001]);
});

test('signed in or not, the catalogue and the public API answer the same (P3, R111)', async ({ browser, request, baseURL }) => {
	const page = await browserWith(browser, [{ id: 1001 }]);
	await signUp(page, request, baseURL!, address('same'));
	for (const path of ['/api/v1/catalogue', '/api/v1/catalogue/search?q=north', '/api/v1/fireground/41', '/api/v1/fireground/43']) {
		const signedIn = await page.request.get(path);
		const signedOut = await request.get(path);
		expect(signedIn.status(), path).toBe(signedOut.status());
		expect(await signedIn.text(), path).toBe(await signedOut.text());
	}
	// And nothing hidden is in the account's answer.
	const body = await (await page.request.get('/api/v1/account')).text();
	expect(body).not.toContain('1003');
	expect(body).not.toContain('Admin');
});

// The first admin (ADR 0025): INITIAL_ADMIN_EMAIL, typed in another case,
// signs up through Authentik's pages; its emailed code proves the address,
// so the account is an admin, and the admin API answers it. As an admin,
// the public API and the catalogue answer exactly as they do anyone, so
// hidden 1003 is absent (A13, the listener half). Another account is a
// listener, which the admin API refuses.
test('the first admin: INITIAL_ADMIN_EMAIL, proven by its code, is an admin who hears and sees what anyone does', async ({ browser, request, baseURL }) => {
	const initial = process.env.RA_INITIAL_ADMIN_EMAIL;
	test.skip(!initial, 'RA_INITIAL_ADMIN_EMAIL is not set');
	test.setTimeout(240_000);
	const page = await browserWith(browser, [{ id: 1001 }]);
	await signUp(page, request, baseURL!, initial!.toUpperCase());
	await expect.poll(async () => (await account(page)).account?.admin, { timeout: 15_000 }).toBe(true);
	const systems = await page.request.get('/api/v1/admin/systems');
	expect(systems.status()).toBe(200);
	expect(await systems.text()).toContain('Example network');
	const hidden = await page.request.get('/api/v1/admin/systems/1/talkgroups/1003');
	expect(hidden.status()).toBe(200);
	for (const path of ['/api/v1/catalogue', '/api/v1/catalogue/search?q=admin', '/api/v1/fireground/41', '/api/v1/fireground/43', '/api/v1/recent-calls']) {
		const asAdmin = await page.request.get(path);
		const anyone = await request.get(path);
		expect(asAdmin.status(), path).toBe(anyone.status());
		const body = await asAdmin.text();
		expect(body, path).toBe(await anyone.text());
		expect(body, path).not.toContain('1003');
	}
	// Signed out, the admin API wants a sign-in; another account is a listener.
	expect((await request.get('/api/v1/admin/systems')).status()).toBe(401);
	const other = await browserWith(browser, [{ id: 1002 }]);
	const otherEmail = address('not-admin');
	await signUp(other, request, baseURL!, otherEmail);
	expect((await account(other)).account?.admin).toBe(false);
	expect((await other.request.get('/api/v1/admin/systems')).status()).toBe(403);
	await expect.poll(async () => (await account(other)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1002 }]);

	// Disabling them (ADR 0033, issue #105) on the Accounts page: their
	// browser is signed out at its next request, and their user is switched
	// off in Authentik itself, which ends its sessions there too.
	await page.goto('/admin/accounts');
	const row = page.getByRole('row').filter({ hasText: otherEmail });
	await row.getByRole('button', { name: /^Disable / }).click();
	await page.getByRole('region', { name: /^Disable .*\?$/ }).getByRole('button', { name: /^Disable / }).click();
	await expect(row).toContainText('Disabled');
	await expect(row).not.toContainText('not yet in Authentik');
	expect((await account(other)).account).toBeNull();
	expect(await authentikUser(request, otherEmail)).toMatchObject({ is_active: false });
	// Enabling: they sign in again, through Authentik's own sign-in (its
	// session ended), with their saved profile as they left it.
	await row.getByRole('button', { name: /^Enable / }).click();
	await expect(row).toContainText('Active');
	expect(await authentikUser(request, otherEmail)).toMatchObject({ is_active: true });
	// Their open page learns it was signed out when it next loads.
	await other.reload();
	await expect(other.getByRole('button', { name: 'Account menu, signed out' })).toBeVisible({ timeout: 15_000 });
	await signIn(other, baseURL!, otherEmail);
	expect((await account(other)).profile?.talkgroups).toEqual([{ id: 1002 }]);
	// The admin can't disable themselves: no button on their own row.
	await expect(page.getByRole('row').filter({ hasText: 'You' }).getByRole('button')).toHaveCount(0);
});

/**
 * The Authentik user with this email, as Authentik's own API says, read
 * with the radio's accounts token (RA_AUTHENTIK_API_TOKEN, the stack's
 * made-up AUTHENTIK_API_TOKEN).
 */
async function authentikUser(request: APIRequestContext, email: string): Promise<{ is_active: boolean } | undefined> {
	const token = process.env.RA_AUTHENTIK_API_TOKEN;
	expect(token, 'RA_AUTHENTIK_API_TOKEN').toBeTruthy();
	const r = await request.get(`${process.env.RA_AUTHENTIK_URL}/api/v3/core/users/`, { params: { email }, headers: { Authorization: `Bearer ${token}` } });
	expect(r.status()).toBe(200);
	return ((await r.json()) as { results: { is_active: boolean }[] }).results[0];
}

test('clearing empties the account and leaves the browser its set; signing out ends both sessions', async ({ browser, request, baseURL }) => {
	const page = await browserWith(browser, [{ id: 1001 }, { id: 1002, priority: true }]);
	const email = address('clear');
	await signUp(page, request, baseURL!, email);
	await expect.poll(async () => (await account(page)).profile?.talkgroups.length, { timeout: 15_000 }).toBe(2);

	await openMenu(page);
	await page.getByRole('button', { name: 'Clear saved settings' }).click();
	const confirm = page.getByRole('region', { name: 'Clear what your account holds?' });
	await expect(confirm).toBeVisible();
	await accessible(page);
	await confirm.getByRole('button', { name: "Clear my account's settings" }).click();
	await expect(page.getByRole('status').filter({ hasText: 'Your account holds nothing now.' })).toBeVisible();
	expect((await account(page)).profile).toBeNull();
	expect(ids(await savedSet(page))).toEqual([1001, 1002]);
	// A page load doesn't put it back.
	await page.reload();
	await expect(page.getByRole('heading', { level: 2, name: 'Your listening set' })).toBeVisible();
	await page.waitForTimeout(1500);
	expect((await account(page)).profile).toBeNull();

	// Signing out: back on the Listen page, signed out, with the set.
	await openMenu(page);
	await page.getByRole('button', { name: 'Sign out' }).click();
	await page.waitForURL((u) => u.origin === new URL(baseURL!).origin && u.pathname === '/', { timeout: 30_000 });
	await expect(page.getByRole('status').filter({ hasText: "You're signed out." })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Account menu, signed out' })).toBeVisible();
	expect((await account(page)).account).toBeNull();
	expect(ids(await savedSet(page))).toEqual([1001, 1002]);
	// Authentik's session ended too: signing in asks who again.
	await openMenu(page);
	await page.getByRole('link', { name: 'Sign in' }).click();
	await expect(page.locator('input[name="uidField"]')).toBeVisible({ timeout: 60_000 });
});

// The download basket goes with the set (R64, R110; ADR 0018): saved to
// the profile, there in another browser, and cleared with the rest. The
// stack has no calls, so the basket's are made-up IDs, which the basket
// shows as no longer available; the conversation is the listener's own.
test('the basket is saved to the account, appears in another browser, and Clear saved settings removes it', async ({ browser, request, baseURL }) => {
	const call = '0f8e2c1a-6b3d-4e5f-9a7b-1c2d3e4f5a6b';
	const page = await browserWith(browser, [{ id: 1001 }]);
	await page.evaluate(
		(id) =>
			localStorage.setItem(
				'radioarchive.basket',
				JSON.stringify({ v: 1, calls: [{ id, added: 1764000000000 }], conversations: [{ id: 'c1', name: 'Structure fire', calls: [{ id, added: 1764000000000 }] }] })
			),
		call
	);
	const email = address('basket');
	await signUp(page, request, baseURL!, email);
	type Saved = { calls: { id: string }[]; conversations: { name: string }[] };
	const basketOf = async (p: Page) => ((await account(p)).profile as unknown as { basket?: Saved } | null)?.basket;
	await expect.poll(async () => (await basketOf(page))?.conversations.map((c) => c.name), { timeout: 15_000 }).toEqual(['Structure fire']);
	// A change in the basket is saved too.
	await page.goto('/basket');
	await page.getByRole('button', { name: 'New conversation' }).click();
	await page.getByLabel('Name required').fill('Grass fire');
	await page.getByRole('button', { name: 'Create' }).click();
	await expect.poll(async () => (await basketOf(page))?.conversations.map((c) => c.name), { timeout: 15_000 }).toEqual(['Structure fire', 'Grass fire']);

	// Another browser, with the same set and an empty basket, signed in to
	// the same account: the basket goes with the set, so it's asked once,
	// and nothing is merged.
	const other = await browserWith(browser, [{ id: 1001 }]);
	await signIn(other, baseURL!, email);
	const prompt = other.getByRole('region', { name: 'Use the listening set saved to your account?' });
	await expect(prompt).toContainText('1 call in its basket');
	await prompt.getByRole('button', { name: "Use my account's set" }).click();
	await other.goto('/basket');
	await expect(other.getByRole('article', { name: 'Structure fire' })).toBeVisible();
	await expect(other.getByRole('article', { name: 'Grass fire' })).toBeVisible();
	await expect(other.locator('[data-state="unavailable"]').first()).toHaveText(/No longer available/);
	await accessible(other);

	// Cleared: the account holds no basket; this browser keeps its own.
	await openMenu(other);
	await other.getByRole('button', { name: 'Clear saved settings' }).click();
	await other.getByRole('region', { name: 'Clear what your account holds?' }).getByRole('button', { name: "Clear my account's settings" }).click();
	await expect(other.getByRole('status').filter({ hasText: 'Your account holds nothing now.' })).toBeVisible();
	expect((await account(other)).profile).toBeNull();
	await expect(other.getByRole('article', { name: 'Structure fire' })).toBeVisible();
	await other.context().close();
	await page.context().close();
});

test('screenshots: the account menu signed out and in, the prompt and the clear', async ({ browser, request, baseURL }) => {
	test.skip(!process.env.RA_SHOTS, 'only when RA_SHOTS names a folder');
	const email = address('shots');
	const setup = await browserWith(browser, [{ id: 1002 }]);
	await signUp(setup, request, baseURL!, email);
	await expect.poll(async () => (await account(setup)).profile?.talkgroups, { timeout: 15_000 }).toEqual([{ id: 1002 }]);
	await setup.context().close();

	for (const width of [1280, 375]) {
		const page = await browserWith(browser, [{ id: 1001 }], width);
		await openMenu(page);
		await shot(page, `account-signed-out-${width}`);
		await signIn(page, baseURL!, email);
		const prompt = page.getByRole('region', { name: 'Use the listening set saved to your account?' });
		await expect(prompt.getByRole('listitem')).toHaveText(['Fireground 41']);
		await shot(page, `first-sign-in-prompt-${width}`);
		await prompt.getByRole('button', { name: "Use my account's set" }).click();
		await openMenu(page);
		await expect(page.getByText('Your listening set, basket and settings are saved to your account.')).toBeVisible();
		await shot(page, `account-signed-in-${width}`);
		await page.getByRole('button', { name: 'Clear saved settings' }).click();
		await expect(page.getByRole('region', { name: 'Clear what your account holds?' })).toBeVisible();
		await shot(page, `clear-confirm-${width}`);
		await page.getByRole('button', { name: "Clear my account's settings" }).click();
		await expect(page.getByRole('status').filter({ hasText: 'Your account holds nothing now.' })).toBeVisible();
		await shot(page, `clear-done-${width}`);
		// Put it back for the next width's prompt.
		const r = await page.request.put('/api/v1/account/profile', {
			headers: { Origin: new URL(baseURL!).origin, 'Content-Type': 'application/json' },
			data: { v: 1, talkgroups: [{ id: 1002 }], alerts: false }
		});
		expect(r.status()).toBe(204);
		await page.context().close();
	}
});
