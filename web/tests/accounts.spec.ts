/// <reference types="node" />
// The Accounts page (issue #105; SPEC.md P3, R55, R84, R111; ADR 0025,
// 0033), signed in from the radio's root URL through the made-up Authentik
// (scripts/ci/e2e-web.sh), whose users API stands in for Authentik's. Each
// test makes people of its own (newPerson), so tests side by side never
// change each other's accounts. The test with Authentik unreachable changes
// what every test's Authentik answers, so it's @live (one at a time).
import { test, expect, type Browser, type Locator, type Page } from '@playwright/test';
import { authentikUsersDown, axe, looks, newPerson, saveLook, shot, signIn, signInAsPerson, themes, widths } from './admin.ts';

/** An account's row on the page, by the name it goes by. */
const row = (page: Page, name: string) => page.getByRole('row').filter({ has: page.getByRole('rowheader', { name, exact: true }) });

/**
 * Opens a row's More actions with the row in the middle of the screen, so
 * the list under the button isn't behind the dock (a test's scrolling puts
 * it at the screen's foot; a person scrolls on).
 */
async function openMore(r: Locator, name: string) {
	await r.evaluate((el) => el.scrollIntoView({ block: 'center' }));
	await r.getByRole('button', { name: `More actions: ${name}` }).click();
}

/** A new browser signed in as a new person of the test's own. */
async function personIn(browser: Browser, word: string) {
	const p = newPerson(word);
	const context = await browser.newContext();
	const page = await context.newPage();
	expect(await signInAsPerson(page, p.key)).toBe(true);
	return { ...p, page };
}

/** Whether the browser is signed in, as its next account request finds. */
async function signedIn(page: Page): Promise<boolean> {
	const r = await page.request.get('/api/v1/account');
	expect(r.status()).toBe(200);
	return ((await r.json()) as { account: unknown }).account != null;
}

async function openAccounts(page: Page) {
	await page.goto('/admin/accounts');
	await expect(page.getByRole('heading', { level: 1, name: 'Accounts' })).toBeVisible();
	await expect(page.getByRole('table', { name: 'Accounts' })).toBeVisible();
}

test('the list: everyone who has signed in, with name, email, role and status, searched in the browser; no actions on your own', async ({ page, browser }) => {
	const sam = await personIn(browser, 'list');
	await signIn(page, 'admin');
	await page.goto('/admin/recorders');
	await page.locator('nav.side').getByRole('link', { name: 'Accounts' }).click();
	await expect(page).toHaveURL(/\/admin\/accounts$/);
	await expect(page).toHaveTitle('Accounts · Admin · RadioArchive');
	await expect(page.getByRole('table', { name: 'Accounts' })).toBeVisible();

	const r = row(page, sam.name);
	await expect(r).toContainText(sam.email);
	await expect(r).toContainText('Listener');
	await expect(r).toContainText('Active');
	await expect(r.getByRole('button', { name: `Disable ${sam.name}` })).toBeVisible();
	const own = row(page, 'Fixture Admin');
	await expect(own).toContainText('Admin');
	await expect(own).toContainText('You');
	await expect(own.getByRole('button')).toHaveCount(0);

	const search = page.getByRole('searchbox', { name: 'Search by name or email' });
	await search.fill(sam.key.toUpperCase());
	await expect(page.getByRole('rowheader')).toHaveCount(1);
	await expect(row(page, sam.name)).toBeVisible();
	await expect(page.getByText(/^1 account of \d+ match\.$/)).toBeVisible();
	await search.fill('nobody-has-this-name');
	await expect(page.getByText('No account matches.')).toBeVisible();
	await sam.page.context().close();
});

test("disabling signs them out at their next request and refuses their sign-in; enabling lets them back in", async ({ page, browser }) => {
	const sam = await personIn(browser, 'disable');
	await signIn(page, 'admin');
	await openAccounts(page);
	const r = row(page, sam.name);
	await r.getByRole('button', { name: `Disable ${sam.name}` }).click();
	const ask = page.getByRole('region', { name: `Disable ${sam.name}?` });
	await expect(ask).toContainText('signed out of the radio at once');
	await expect(ask).toContainText('saved settings are kept');
	// Cancelling changes nothing.
	await ask.getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByText(`Nothing changed for ${sam.name}.`)).toBeVisible();
	await expect(r).toContainText('Active');

	await r.getByRole('button', { name: `Disable ${sam.name}` }).click();
	await page.getByRole('region', { name: `Disable ${sam.name}?` }).getByRole('button', { name: `Disable ${sam.name}`, exact: true }).click();
	await expect(page.getByText(`Disabled ${sam.name}: they're signed out and can't sign in until they're enabled.`)).toBeVisible();
	await expect(r).toContainText('Disabled');
	await expect(r).not.toContainText('not yet in Authentik');

	// Their browser is signed out at its next request; Authentik refuses them.
	expect(await signedIn(sam.page)).toBe(false);
	expect(await signInAsPerson(sam.page, sam.key)).toBe(false);

	await r.getByRole('button', { name: `Enable ${sam.name}` }).click();
	await expect(page.getByText(`Enabled ${sam.name}: they can sign in again.`)).toBeVisible();
	await expect(r).toContainText('Active');
	expect(await signInAsPerson(sam.page, sam.key)).toBe(true);
	await sam.page.context().close();
});

test('roles: an admin makes another account an admin, and a listener again, asking first', async ({ page, browser }) => {
	const sam = await personIn(browser, 'role');
	await signIn(page, 'admin');
	await openAccounts(page);
	const r = row(page, sam.name);
	await openMore(r, sam.name);
	await r.getByRole('button', { name: 'Make an admin…' }).click();
	await page.getByRole('region', { name: `Make ${sam.name} an admin?` }).getByRole('button', { name: `Make ${sam.name} an admin` }).click();
	await expect(page.getByText(`${sam.name} is an admin now.`)).toBeVisible();
	await expect(r).toContainText('Admin');
	await expect(r).not.toContainText('Listener');
	await openMore(r, sam.name);
	await r.getByRole('button', { name: 'Make a listener…' }).click();
	await page.getByRole('region', { name: `Make ${sam.name} a listener?` }).getByRole('button', { name: `Make ${sam.name} a listener` }).click();
	await expect(page.getByText(`${sam.name} is a listener now.`)).toBeVisible();
	await expect(r).toContainText('Listener');
	await sam.page.context().close();
});

test('deleting asks, naming what goes; the account, its sessions and its sign-in are gone; on phones the question is a dialog', async ({ page, browser }) => {
	test.setTimeout(90_000);
	const sam = await personIn(browser, 'delete');
	const kim = await personIn(browser, 'delete-phone');
	await signIn(page, 'admin');
	await openAccounts(page);
	const r = row(page, sam.name);
	await openMore(r, sam.name);
	await r.getByRole('button', { name: 'Delete account…' }).click();
	const ask = page.getByRole('region', { name: `Delete ${sam.name}'s account?` });
	await expect(ask).toContainText(sam.email);
	await expect(ask).toContainText('saved settings');
	await expect(ask).toContainText("can't be undone");
	await ask.getByRole('button', { name: `Delete ${sam.name}'s account` }).click();
	await expect(page.getByText(`Deleted ${sam.name}'s account.`)).toBeVisible();
	await expect(row(page, sam.name)).toHaveCount(0);
	expect(await signedIn(sam.page)).toBe(false);
	// Their Authentik user is gone too: they can't sign in.
	expect(await signInAsPerson(sam.page, sam.key)).toBe(false);

	await page.setViewportSize({ width: 375, height: 800 });
	const k = row(page, kim.name);
	await openMore(k, kim.name);
	await k.getByRole('button', { name: 'Delete account…' }).click();
	const dialog = page.getByRole('dialog', { name: `Delete ${kim.name}'s account?` });
	await expect(dialog).toBeVisible();
	await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused();
	await dialog.getByRole('button', { name: `Delete ${kim.name}'s account` }).click();
	await expect(page.getByText(`Deleted ${kim.name}'s account.`)).toBeVisible();
	await sam.page.context().close();
	await kim.page.context().close();
});

test('@live with Authentik unreachable, disabling still locks them out of the radio, the page says so, and trying again reaches Authentik', async ({ page, browser }) => {
	const sam = await personIn(browser, 'down');
	await signIn(page, 'admin');
	await openAccounts(page);
	const r = row(page, sam.name);
	try {
		await authentikUsersDown(page, true);
		await r.getByRole('button', { name: `Disable ${sam.name}` }).click();
		await page.getByRole('region', { name: `Disable ${sam.name}?` }).getByRole('button', { name: `Disable ${sam.name}`, exact: true }).click();
		await expect(page.getByRole('alert').filter({ hasText: "Authentik couldn't be reached just now" })).toContainText('Disabled in the radio');
		await expect(r).toContainText('Disabled here, not yet in Authentik');
		await shot(page, 'accounts-pending-harbour-light-1280');
		// Locked out of the radio: signed out, and the radio refuses the
		// sign-in Authentik still lets through.
		expect(await signedIn(sam.page)).toBe(false);
		expect(await signInAsPerson(sam.page, sam.key)).toBe(false);
		await expect(sam.page.getByRole('alert')).toHaveText('This account has been turned off here, so you weren\'t signed in. Listening works as before.');

		await authentikUsersDown(page, false);
		await openMore(r, sam.name);
		await r.getByRole('button', { name: 'Try Authentik again' }).click();
		await expect(page.getByText(`Authentik has it now: ${sam.name} is disabled there too.`)).toBeVisible();
		await expect(r).toContainText('Disabled');
		await expect(r).not.toContainText('not yet in Authentik');
		await expect(page.getByRole('alert').filter({ hasText: 'Authentik' })).toHaveCount(0);
	} finally {
		await authentikUsersDown(page, false);
	}
	await sam.page.context().close();
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the Accounts page, its More actions and the delete question, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL, browser }) => {
				test.setTimeout(180_000);
				const sam = await personIn(browser, 'axe');
				await saveLook(context, baseURL!, look, theme);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				const tag = `${look}-${theme}-${width}`;
				await openAccounts(page);
				await page.getByRole('searchbox', { name: 'Search by name or email' }).fill('made up axe');
				const r = row(page, sam.name);
				await expect(r).toBeVisible();
				await page.getByRole('searchbox', { name: 'Search by name or email' }).fill('');
				await axe(page, width);
				await shot(page, `accounts-${tag}`);

				await openMore(r, sam.name);
				await expect(r.getByRole('button', { name: 'Delete account…' })).toBeVisible();
				await axe(page, width);
				await shot(page, `accounts-more-${tag}`);
				await r.getByRole('button', { name: 'Delete account…' }).click();
				const ask = width === 375 ? page.getByRole('dialog', { name: `Delete ${sam.name}'s account?` }) : page.getByRole('region', { name: `Delete ${sam.name}'s account?` });
				await expect(ask).toBeVisible();
				await axe(page, width);
				await shot(page, `accounts-delete-${tag}`);
				await ask.getByRole('button', { name: 'Cancel' }).click();
				await expect(page.getByText(`Nothing changed for ${sam.name}.`)).toBeVisible();
				await axe(page, width);
				await sam.page.context().close();
			});
