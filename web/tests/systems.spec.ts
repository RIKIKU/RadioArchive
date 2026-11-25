/// <reference types="node" />
// The Systems page (issue #114; SPEC.md R11, R12, R21, R22, A36; ADR 0003,
// 0006, 0034), signed in as the fixture's admin from the radio's root URL
// through the made-up Authentik: the fixture's EX with Bench's and Ridge's
// `ex` attached, its tags and group North. The tests here change nothing
// (refusals, and questions cancelled); the ones that do (attaching,
// auto-create, a tag's LED colour) are systems-live.spec.ts's.
import { test, expect, type Page } from '@playwright/test';
import { axe, looks, saveLook, shot, signIn, themes, widths } from './admin.ts';

const table = (page: Page, name: string) => page.getByRole('table', { name });
const row = (page: Page, name: string, header: string) =>
	table(page, name)
		.locator('tbody tr')
		.filter({ has: page.locator('th', { hasText: new RegExp(`^\\s*${header.replace(/[/]/g, '\\/')}\\s*$`) }) });

async function open(page: Page) {
	await signIn(page, 'admin');
	await page.goto('/admin/systems');
	await expect(row(page, 'Systems', '1')).toContainText('EX');
}

test('the Systems page: EX with auto-create off, its attachments, tags and groups; in the Admin menu after Units', async ({ page }) => {
	await open(page);
	await expect(page).toHaveTitle('Systems · Admin · RadioArchive');
	const side = page.locator('nav.side');
	await expect(side.getByRole('link').filter({ hasText: /^(Talkgroups|Units|Systems|Recorders|Accounts|Audit log|Settings)$/ })).toHaveText([
		'Talkgroups',
		'Units',
		'Systems',
		'Recorders',
		'Accounts',
		'Audit log',
		'Settings'
	]);
	await expect(side.getByRole('link', { name: 'Systems' })).toHaveAttribute('aria-current', 'page');
	const ex = row(page, 'Systems', '1');
	await expect(ex).toContainText('Example network');
	await expect(ex).toContainText('Off');
	await expect(row(page, 'Attachments', 'Bench / ex')).toContainText('EX');
	await expect(row(page, 'Attachments', 'Ridge / ex')).toContainText('EX');
	await expect(page.getByRole('region', { name: 'Attachments' })).toContainText('A change affects only future calls');
	for (const t of ['dispatch', 'fireground', 'admin']) await expect(row(page, 'Tags', t)).toBeVisible();
	await expect(row(page, 'Groups', 'North')).toBeVisible();
});

test("deleting EX while it has talkgroups is refused with the server's message; nothing changes", async ({ page }) => {
	await open(page);
	await page.getByRole('button', { name: 'More actions: system EX' }).click();
	await page.getByRole('button', { name: 'Delete…' }).click();
	const dialog = page.getByRole('dialog', { name: 'Delete system EX?' });
	await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused();
	await dialog.getByRole('button', { name: 'Delete system EX' }).click();
	await expect(dialog).toContainText("System EX still has talkgroups or calls, so it can't be deleted.");
	await dialog.getByRole('button', { name: 'Cancel' }).click();
	await expect(dialog).toHaveCount(0);
	expect((await page.request.get('/api/v1/admin/systems/1')).status()).toBe(200);
	await expect(row(page, 'Systems', '1')).toBeVisible();
});

test("a system's and a group's form: problems under their fields, the server's refusals by field, and auto-create asks first", async ({ page }) => {
	await open(page);
	// A new system: the ID and label required; EX's ID and label are taken.
	await page.getByRole('button', { name: 'Add a system' }).click();
	const add = page.getByRole('region', { name: 'Add a system' });
	await expect(add.getByRole('heading', { level: 2 })).toBeFocused();
	await add.getByRole('button', { name: 'Add the system' }).click();
	await expect(add.locator('p.error')).toHaveText('2 fields need attention');
	await expect(add.getByRole('textbox', { name: 'ID required' })).toBeFocused();
	await add.getByRole('textbox', { name: 'ID required' }).fill('1');
	await add.getByRole('textbox', { name: 'Label required' }).fill('EX-test');
	await add.getByRole('button', { name: 'Add the system' }).click();
	await expect(add.locator('#system-edit-id-error')).toHaveText('A system with ID 1 already exists.');
	await add.getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByRole('button', { name: 'Add a system' })).toBeFocused();

	// Editing EX: switching auto-create on asks first, inline; cancelled, nothing is sent.
	const sent: string[] = [];
	page.on('request', (r) => {
		if (r.method() !== 'GET' && r.url().includes('/api/v1/admin/')) sent.push(`${r.method()} ${new URL(r.url()).pathname}`);
	});
	await page.getByRole('button', { name: 'Edit system EX' }).click();
	const edit = page.getByRole('region', { name: 'Edit system EX' });
	await expect(row(page, 'Systems', '1')).toHaveAttribute('data-open', 'edit');
	await expect(edit.getByRole('textbox', { name: 'ID required' })).toHaveCount(0);
	await edit.getByRole('checkbox', { name: 'Auto-create talkgroups' }).check();
	await edit.getByRole('button', { name: 'Save' }).click();
	const ask = edit.getByRole('region', { name: 'Switch auto-create on for EX?' });
	await expect(ask.getByRole('heading')).toBeFocused();
	await expect(ask).toContainText('public and unreviewed');
	await ask.getByRole('button', { name: 'Cancel' }).click();
	await expect(edit.getByRole('checkbox', { name: 'Auto-create talkgroups' })).toBeFocused();
	await edit.getByRole('checkbox', { name: 'Auto-create talkgroups' }).uncheck();
	await edit.getByRole('button', { name: 'Save' }).click();
	await expect(page.locator('p.status')).toHaveText('Nothing to change on EX.');
	await expect(page.getByRole('button', { name: 'Edit system EX' })).toBeFocused();
	expect(sent).toEqual([]);
	expect(((await (await page.request.get('/api/v1/admin/systems/1')).json()) as { auto_create: boolean }).auto_create).toBe(false);

	// A group: a name is required, and North's is taken.
	await page.getByRole('button', { name: 'Add a group' }).click();
	const group = page.getByRole('region', { name: 'Add a group' });
	await group.getByRole('button', { name: 'Add the group' }).click();
	await expect(group.locator('#group-edit-name-error')).toHaveText('Give the group a name.');
	await group.getByRole('textbox', { name: 'Name required' }).fill('North');
	await group.getByRole('button', { name: 'Add the group' }).click();
	await expect(group.locator('#group-edit-name-error')).toHaveText('A group named "North" already exists.');
	await expect(group.getByRole('textbox', { name: 'Name required' })).toBeFocused();
	await group.getByRole('button', { name: 'Cancel' }).click();
});

test('the Systems page refuses signed out and a listener, as the admin API does', async ({ page }) => {
	await page.goto('/admin/systems');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('This page is for admins');
	await signIn(page, 'listener');
	await page.goto('/admin/systems');
	await expect(page.locator('main')).toHaveText('This page is for admins');
});

/** A pair that isn't attached, with its rejected calls, as /rejections gives it after Bench / other's calls (systems-live.spec.ts makes real ones). */
async function withUnattached(page: Page) {
	await page.route('**/api/v1/admin/rejections', async (route) => {
		const r = await route.fetch();
		const body = (await r.json()) as { unattached: object[] };
		body.unattached = [{ recorder: 'Bench', short_name: 'other', calls: 2, last_at: new Date().toISOString(), message: 'Bench / other: 2 calls rejected, not attached to a system' }];
		await route.fulfill({ response: r, json: body });
	});
}

for (const look of looks) {
	for (const theme of themes) {
		for (const width of widths) {
			test(`axe: the Systems page with attachments, a pair not attached, a tag being edited and the dialog, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(90_000);
				await saveLook(context, baseURL!, look, theme);
				await page.setViewportSize({ width, height: 900 });
				await withUnattached(page);
				await open(page);
				const tag = `${look}-${theme}-${width}`;
				await expect(page.getByRole('list', { name: 'Not attached' })).toContainText('Bench / other: 2 calls rejected, not attached to a system');
				await axe(page, width);
				await shot(page, `systems-${tag}`);
				// A tag being edited, with a problem; the system's form with auto-create's question.
				await page.getByRole('button', { name: 'Edit tag fireground' }).click();
				const t = page.getByRole('region', { name: 'Edit tag fireground' });
				await t.getByRole('textbox', { name: 'Sort order required' }).fill('first');
				await t.getByRole('button', { name: 'Save' }).click();
				await expect(t.locator('#tag-edit-sort_order-error')).toBeVisible();
				await axe(page, width);
				await shot(page, `systems-editing-${tag}`);
				await t.getByRole('button', { name: 'Cancel' }).click();
				await page.getByRole('button', { name: 'Edit system EX' }).click();
				const s = page.getByRole('region', { name: 'Edit system EX' });
				await s.getByRole('checkbox', { name: 'Auto-create talkgroups' }).check();
				await s.getByRole('button', { name: 'Save' }).click();
				const ask = s.getByRole('region', { name: 'Switch auto-create on for EX?' });
				await expect(ask).toBeVisible();
				await axe(page, width);
				await ask.getByRole('button', { name: 'Cancel' }).click();
				await s.getByRole('button', { name: 'Cancel' }).click();
				// More actions open, and the remove dialog.
				await page.getByRole('button', { name: 'More actions: Bench / ex' }).click();
				await axe(page, width);
				await page.getByRole('button', { name: 'Remove…' }).click();
				await expect(page.getByRole('dialog', { name: "Remove Bench / ex's attachment?" })).toBeVisible();
				await axe(page, width);
				await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
			});
		}
	}
}
