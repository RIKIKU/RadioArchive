/// <reference types="node" />
// The Audit log page and the audit log's retention on the Settings page
// (issue #112; SPEC.md R54, R84, A26 browser half; ADR 0029, 0032), signed
// in from the radio's root URL through the made-up Authentik. The entries
// they find are the fixture's made-up past (testing/webfixture's history),
// days old, so what other tests change meanwhile, which is newer, doesn't
// move them: by two admins, with a browser, the module and an import, and
// the nightly run's prune. (That reading the log adds nothing to it is the
// Go tests' and TestIntegrationAuditHarness's: other tests here change
// things meanwhile.) The tests
// that change what other tests see (saving the period, renaming 1001) are
// admin-live.spec.ts's.
import { test, expect, type Page } from '@playwright/test';
import { axe, inLook, looks, ownLook, saveLook, shot, signIn, themes, widths } from './admin.ts';

const table = (page: Page) => page.getByRole('table', { name: 'Audit log' });
const body = (page: Page) => table(page).getByRole('rowgroup').last().getByRole('row');
const SECOND = 'Second Admin (second-admin@example.test)';

/** The made-up entries' days on the deployment's clock, by how many days ago each was written. */
async function fixtureDays(page: Page): Promise<Map<number, string>> {
	const zone = ((await (await page.request.get('/api/v1/catalogue')).json()) as { time_zone: string }).time_zone;
	const a = (await (await page.request.get('/api/v1/admin/audit?who=fixture-second-admin')).json()) as { entries: { at: string; kind: string }[] };
	const day = (iso: string) => new Intl.DateTimeFormat('en-CA', { timeZone: zone, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(iso));
	const byKind = new Map(a.entries.map((e) => [e.kind, day(e.at)]));
	return new Map([
		[10, byKind.get('talkgroup')!],
		[9, byKind.get('unit')!],
		[5, byKind.get('account')!]
	]);
}

test('the Audit log page: in the Admin menu, each change in words with who, how and when, filtered by who and kind', async ({ page }) => {
	await signIn(page, 'admin');
	const side = page.locator('nav.side');
	await expect(side.getByRole('link').filter({ hasText: /^(Recorders|Audit log|Settings)$/ })).toHaveText(['Recorders', 'Audit log', 'Settings']);
	await side.getByRole('link', { name: 'Audit log' }).click();
	await expect(page).toHaveURL(/\/admin\/audit$/);
	await expect(page).toHaveTitle('Audit log · Admin · RadioArchive');
	await expect(page.getByRole('heading', { level: 1, name: 'Audit log' })).toBeVisible();
	await expect(page.getByRole('main')).toContainText('Entries are kept for 730 days');
	await expect(table(page).getByRole('columnheader', { name: 'When' })).toHaveAttribute('aria-sort', 'descending');

	// Who: everyone with an entry, the system's prune too.
	const who = page.getByRole('combobox', { name: 'Who' });
	await expect(who.getByRole('option', { name: SECOND })).toBeAttached();
	await expect(who.getByRole('option', { name: 'System', exact: true })).toBeAttached();
	await who.selectOption({ label: SECOND });
	await expect(page).toHaveURL(/\?who=fixture-second-admin$/);
	await expect(body(page)).toHaveCount(3);
	const [sessions, unit, renamed] = [body(page).nth(0), body(page).nth(1), body(page).nth(2)];
	await expect(sessions).toContainText('Signed out everywhere: 2 sessions ended');
	await expect(sessions).toContainText('Browser');
	await expect(sessions).toContainText('Fixture Listener');
	await expect(unit).toContainText('Import');
	await expect(unit.getByRole('listitem')).toHaveText(['Created', 'System 1', 'From unit 5100', 'To unit 5199', 'Alias Portables']);
	await expect(renamed).toContainText('PowerShell module');
	await expect(renamed).toContainText('Second Admin');
	await expect(renamed).toContainText('second-admin@example.test');
	await expect(renamed).toContainText('Talkgroup EX 1002 FG 41');
	await expect(renamed.getByRole('listitem')).toHaveText('Name Fireground → Fireground 41');
	await expect(renamed.locator('time')).toHaveText(/^[A-Z][a-z]{2},? \d+ [A-Z][a-z]{2},? \d\d:\d\d$/);
	await expect(page.getByRole('status').filter({ hasText: 'shown' })).toHaveText('3 changes shown, newest first.');

	// Kind: Anyone's attachments.
	await who.selectOption({ label: 'Anyone' });
	await page.getByRole('combobox', { name: 'Kind of thing' }).selectOption({ label: 'Attachment' });
	await expect(page).toHaveURL(/\?kind=attachment$/);
	const att = body(page).filter({ hasText: 'Bench / ex attached to EX' });
	await expect(att).toHaveCount(1);
	await expect(att).toContainText('Fixture Admin');
	await expect(att).toContainText('fixture-admin@example.test');

	// Nothing matches: says so.
	await page.goto('/admin/audit?who=nobody-at-all');
	await expect(table(page)).toHaveCount(0);
	await expect(page.getByText('No changes match these filters.')).toBeVisible();
});

test("a row's History: only that thing's changes, and back", async ({ page }) => {
	await signIn(page, 'admin');
	await page.goto('/admin/audit');
	const row = body(page).filter({ hasText: 'Label DISP → DISP N' });
	await row.getByRole('button', { name: 'History of talkgroup EX 1001 DISP N' }).click();
	await expect(page).toHaveURL(/\?kind=talkgroup&thing=1%2F1001$/);
	await expect(page.getByText('Only changes to Talkgroup EX 1001 DISP N')).toBeVisible();
	const back = page.getByRole('button', { name: 'Show every talkgroup' });
	await expect(back).toBeFocused();
	await expect(body(page).filter({ hasText: 'Label DISP → DISP N' })).toHaveCount(1);
	for (const r of await body(page).all()) await expect(r).toContainText(/EX 1001/);
	await back.click();
	await expect(page).toHaveURL(/\?kind=talkgroup$/);
	await expect(body(page).filter({ hasText: 'EX 1002 FG 41' })).not.toHaveCount(0);
	await expect(page.getByRole('combobox', { name: 'Kind of thing' })).toBeFocused();
});

test('When, oldest first, asks the server; Load more pages on with its cursor', async ({ page }) => {
	await signIn(page, 'admin');
	// Pages of 3, so the made-up past is pages enough.
	await page.route((url) => url.pathname === '/api/v1/admin/audit', (route) => {
		const u = new URL(route.request().url());
		u.searchParams.set('limit', '3');
		return route.continue({ url: u.toString() });
	});
	await page.goto('/admin/audit');
	await table(page).getByRole('button', { name: 'When' }).click();
	await expect(table(page).getByRole('columnheader', { name: 'When' })).toHaveAttribute('aria-sort', 'ascending');
	await expect(page).toHaveURL(/\?order=oldest$/);
	await expect(body(page)).toHaveCount(3);
	const prune = body(page).first();
	await expect(prune).toContainText('Deleted 12 entries from before 16 Oct 2024 (kept 730 days)');
	await expect(prune).toContainText('System');
	await expect(prune).toContainText('Nightly run');
	await expect(prune).toContainText('Audit log');
	await expect(page.getByRole('status').filter({ hasText: 'shown' })).toHaveText('3 changes shown, oldest first; there are more.');
	await page.getByRole('button', { name: 'Load more' }).click();
	await expect(body(page)).toHaveCount(6);
	await expect(body(page).nth(3)).toContainText('Bench / ex attached to EX');
	await expect(body(page).nth(4)).toContainText('State public → hidden');
	await expect(body(page).nth(5)).toContainText('Quiet after (minutes) none → 1');
	// Again: newest first.
	await table(page).getByRole('button', { name: 'When' }).click();
	await expect(page).toHaveURL(/\/admin\/audit$/);
	await expect(body(page)).toHaveCount(3);
	await expect(body(page).first()).not.toContainText('Deleted 12 entries');
});

test('the days: a range on the deployment\'s clock, linked from the URL, and Any time', async ({ page }) => {
	await signIn(page, 'admin');
	const days = await fixtureDays(page);
	await page.goto(`/admin/audit?from=${days.get(10)}&to=${days.get(9)}`);
	await expect(page.getByRole('button', { name: /^Dates: / })).toBeVisible();
	await expect(body(page)).toHaveCount(2);
	await expect(body(page).nth(0)).toContainText('Alias Portables');
	await expect(body(page).nth(1)).toContainText('Name Fireground → Fireground 41');
	// One day, by who as well.
	await page.goto(`/admin/audit?who=fixture-second-admin&from=${days.get(5)}`);
	await expect(body(page)).toHaveCount(1);
	await expect(body(page)).toContainText('Signed out everywhere');
	await page.getByRole('button', { name: 'Any time' }).click();
	await expect(page).toHaveURL(/\?who=fixture-second-admin$/);
	await expect(body(page)).toHaveCount(3);
	// The calendar opens from Dates.
	await page.getByRole('button', { name: 'Dates', exact: true }).click();
	await expect(page.getByRole('dialog')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(page.getByRole('dialog')).toHaveCount(0);
});

/** The admin API's answer to a shorter audit period, as if it deleted 3 entries. */
async function threeEntriesOld(page: Page) {
	await page.route('**/api/v1/admin/audit/retention', async (route) => {
		const r = route.request();
		const sent = r.method() === 'PUT' ? (r.postDataJSON() as { days: number; confirm?: boolean }) : undefined;
		if (!sent || sent.confirm || sent.days >= 730) return route.continue();
		return route.fulfill({
			status: 409,
			json: { error: 'confirm_shorter_audit_retention', message: 'Confirm?', days: sent.days, entries: 3 }
		});
	});
}

test("Settings: the audit log's period, a problem under its field, and asking before a shorter one deletes entries", async ({ page }) => {
	await signIn(page, 'admin');
	await threeEntriesOld(page);
	await page.goto('/admin/settings');
	await expect(page.getByRole('heading', { level: 2, name: 'Audit log' })).toBeVisible();
	const days = page.getByRole('textbox', { name: 'Keep audit log entries (days) required' });
	await expect(days).toHaveValue('730');
	await expect(days).toHaveAttribute('required', '');
	await expect(page.locator('#audit-days-hint')).toContainText(/From 30 to 3,650 days; 730 by default\..*The log holds [\d,]+ entr(y|ies), the oldest from /);
	const save = page.getByRole('button', { name: "Save the audit log's retention" });
	await days.fill('29');
	await save.click();
	await expect(days).toHaveAttribute('aria-invalid', 'true');
	await expect(days).toBeFocused();
	await expect(page.locator('#audit-days-error')).toHaveText('Enter whole days from 30 to 3,650.');
	await days.fill('730');
	await save.click();
	await expect(page.locator('p.audit-status')).toHaveText('Nothing to change: audit log entries are kept for 730 days.');
	await expect(days).not.toHaveAttribute('aria-invalid', /./);

	await days.fill('30');
	await save.click();
	const confirm = page.getByRole('region', { name: 'Delete 3 audit log entries?' });
	await expect(confirm).toBeVisible();
	await expect(confirm.getByRole('heading')).toBeFocused();
	await expect(confirm).toContainText('Keeping audit log entries for 30 days instead of 730 days deletes 3 entries at the next nightly run');
	await expect(confirm.getByRole('button', { name: 'Shorten to 30 days' })).toBeVisible();
	await confirm.getByRole('button', { name: 'Cancel' }).click();
	await expect(confirm).toHaveCount(0);
	await expect(page.locator('p.audit-status')).toHaveText('Nothing changed: audit log entries are still kept for 730 days.');
	await expect(days).toHaveValue('730');
	await expect(days).toBeFocused();
	await page.unrouteAll();
	expect(((await (await page.request.get('/api/v1/admin/audit/retention')).json()) as { days: number }).days).toBe(730);
});

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the Audit log page, its calendar and filters, and Settings' audit period asking, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(90_000);
				await saveLook(context, baseURL!, look, theme);
				await ownLook(page);
				await signIn(page, 'admin');
				await page.setViewportSize({ width, height: 900 });
				const tag = `${look}-${theme}-${width}`;

				await page.goto('/admin/audit?who=fixture-second-admin');
				await expect(body(page)).toHaveCount(3);
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `audit-${tag}`);

				// The made-up past, oldest first, with a row's History.
				await page.goto('/admin/audit?order=oldest');
				await expect(body(page).first()).toContainText('Deleted 12 entries');
				await inLook(page, look, theme);
				await axe(page, width);
				await body(page).filter({ hasText: 'Label DISP → DISP N' }).getByRole('button', { name: /^History of / }).click();
				await expect(page.getByRole('button', { name: 'Show every talkgroup' })).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `audit-history-${tag}`);

				await page.getByRole('button', { name: 'Dates', exact: true }).click();
				await expect(page.getByRole('dialog')).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await page.keyboard.press('Escape');

				await page.goto('/admin/audit?who=nobody-at-all');
				await expect(page.getByText('No changes match these filters.')).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);

				// Settings, asking before a shorter audit period deletes entries; cancelled.
				await threeEntriesOld(page);
				await page.goto('/admin/settings');
				await page.getByRole('textbox', { name: /Keep audit log entries/ }).fill('30');
				await page.getByRole('button', { name: "Save the audit log's retention" }).click();
				const confirm = page.getByRole('region', { name: 'Delete 3 audit log entries?' });
				await expect(confirm).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
				await shot(page, `settings-audit-${tag}`);
				await confirm.getByRole('button', { name: 'Cancel' }).click();
				await page.getByRole('textbox', { name: /Keep audit log entries/ }).fill('');
				await page.getByRole('button', { name: "Save the audit log's retention" }).click();
				await expect(page.locator('#audit-days-error')).toBeVisible();
				await inLook(page, look, theme);
				await axe(page, width);
			});
