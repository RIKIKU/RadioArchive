/// <reference types="node" />
// What the admin pages' tests (issue #111) share: signing in as a user
// does, from the radio's root URL and its account menu, through the
// made-up Authentik scripts/ci/e2e-web.sh runs (testing/fakeauthentik), as
// the fixture's admin or as a listener; the looks; axe; and the pull
// request's screenshots.
import { expect, test, type BrowserContext, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

export const looks = ['harbour', 'console'] as const;
export const themes = ['light', 'dark'] as const;
export const widths = [375, 1280] as const;

/** The made-up Authentik (RA_AUTHENTIK_URL, set by e2e-web.sh). */
function authentik(): URL {
	const v = process.env.RA_AUTHENTIK_URL;
	if (!v) throw new Error("RA_AUTHENTIK_URL isn't set: run the admin pages' tests through scripts/ci/e2e-web.sh");
	return new URL(v);
}

/**
 * Signs in from the root URL's account menu: as the fixture's admin, or as
 * a listener, whom the made-up Authentik signs in when its person cookie
 * says so (on its own /authorize only).
 */
export async function signIn(page: Page, as: 'admin' | 'listener') {
	const ak = authentik();
	if (as === 'listener') {
		await page.context().addCookies([{ name: 'fakeauthentik_person', value: 'listener', domain: ak.hostname, path: '/authorize' }]);
	}
	await page.goto('/');
	await page.getByRole('button', { name: 'Account menu, signed out' }).click();
	await page.getByRole('link', { name: 'Sign in' }).click();
	await expect(page.getByRole('button', { name: as === 'admin' ? 'Account menu, Fixture Admin' : 'Account menu, Fixture Listener' })).toBeVisible({ timeout: 15_000 });
	await expect.poll(async () => ((await (await page.request.get('/api/v1/account')).json()) as { account?: { admin?: boolean } }).account?.admin).toBe(as === 'admin');
}

/**
 * A person of the test's own (made-up Authentik's "person-<word>", ADR
 * 0033): subject `person-<word>`, email `person-<word>@example.test`, name
 * "Made Up <words>". Each test that changes an account makes its own, so
 * tests running side by side never change each other's.
 */
export function newPerson(word: string) {
	const key = `person-${word}-${test.info().project.name}-${Math.random().toString(36).slice(2, 7)}`;
	return { key, email: `${key}@example.test`, name: `Made Up ${key.slice('person-'.length).replaceAll('-', ' ')}` };
}

/**
 * Tries to sign in from the root URL's account menu as the test's own
 * person (newPerson), returning whether the radio signed them in.
 */
export async function signInAsPerson(page: Page, key: string): Promise<boolean> {
	const ak = authentik();
	await page.context().addCookies([{ name: 'fakeauthentik_person', value: key, domain: ak.hostname, path: '/authorize' }]);
	await page.goto('/');
	await page.getByRole('button', { name: 'Account menu, signed out' }).click();
	// The callback's answer sends the browser on: home signed in, or to a notice.
	const back = page.waitForResponse((r) => new URL(r.url()).pathname === '/auth/callback', { timeout: 15_000 });
	await page.getByRole('link', { name: 'Sign in' }).click();
	await back;
	await page.waitForLoadState('load');
	await expect(page.getByRole('button', { name: /^Account menu, / })).toBeVisible();
	return ((await (await page.request.get('/api/v1/account')).json()) as { account?: { id?: string } | null }).account?.id === key;
}

/** Makes the made-up Authentik's users API unreachable (true) or back (false). */
export async function authentikUsersDown(page: Page, down: boolean) {
	const r = await page.request.post(new URL(`/fake/users-down?down=${down ? 1 : 0}`, authentik()).toString());
	expect(r.status()).toBe(204);
}

/** The look and theme a browser saved (R82), sent with the page. */
export async function saveLook(context: BrowserContext, baseURL: string, look: string, theme: string) {
	const url = new URL(baseURL).origin;
	await context.addCookies([
		{ name: 'look', value: look, url },
		{ name: 'theme', value: theme, url }
	]);
}

export async function axe(page: Page, width: number) {
	// Nothing wider than the screen.
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
}

/** The pull request's screenshots (RA_SHOTS names a folder), the whole page in one screen. */
export async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS || test.info().project.name !== 'chromium') return;
	const size = page.viewportSize()!;
	await page.mouse.move(0, 0);
	await page.evaluate(() => window.scrollTo(0, 0));
	const height = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: size.width, height: Math.max(height, size.height) });
	await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}.png` });
	await page.setViewportSize(size);
}

/** A recorder's card on the Recorders page. */
export const recorderCard = (page: Page, name: string) => page.getByRole('listitem').filter({ has: page.getByRole('heading', { level: 2, name, exact: true }) });
