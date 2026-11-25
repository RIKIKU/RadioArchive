/// <reference types="node" />
// The Units page (issue #114; SPEC.md R26, R27, A36; ADR 0019, 0034),
// signed in as the fixture's admin from the radio's root URL through the
// made-up Authentik.
//
// R26 through the page: the fixture's 5001 "Engine 1" and 5100-5199
// "Portables" are in the table, and its "which alias" answers 5150 as
// Portables and 5200 by its ID. The adding, editing and deleting are on
// radio IDs of the test's own (6000s, a block per browser, since a run by
// hand shares one database between them), never heard on any call, so unit
// search and the other tests see nothing of them.
import { test, expect, type Page } from '@playwright/test';
import { axe, looks, saveLook, shot, signIn, themes, widths } from './admin.ts';

const rowsOf = (page: Page) => page.locator('main table[role="table"] tbody tr');
const row = (page: Page, unit: string) => rowsOf(page).filter({ has: page.locator('th', { hasText: new RegExp(`^\\s*${unit}\\s*$`) }) });
const count = (page: Page) => page.locator('p.count');

/** The test's own block of radio IDs: 6000, 6300 or 6600. */
const base = () => 6000 + 300 * ['chromium', 'firefox', 'webkit'].indexOf(test.info().project.name);

/** Deletes the test's own units, should an earlier try have left them. */
async function clean(page: Page, b: number) {
	for (const u of [`${b + 1}`, `${b + 100}-${b + 199}`]) await page.request.delete(`/api/v1/admin/systems/1/units/${u}`);
}

async function open(page: Page) {
	await signIn(page, 'admin');
	await page.goto('/admin/units');
	await expect(count(page)).toHaveText(/^Showing \d+ of \d+ units?\.$/);
}

test('R26 through the page: 5001 "Engine 1" and the range 5100-5199 "Portables"; 5150 shows as Portables, 5200 as its ID', async ({ page }) => {
	await open(page);
	await expect(page.getByRole('heading', { level: 1, name: 'Units' })).toBeVisible();
	await expect(page).toHaveTitle('Units · Admin · RadioArchive');
	await expect(row(page, '5001')).toContainText('Engine 1');
	await expect(row(page, '5100-5199')).toContainText('Portables');
	await expect(row(page, '5100-5199')).toContainText('100');

	const radio = page.getByRole('textbox', { name: 'Radio ID', exact: true });
	const answer = page.locator('p.answer');
	await radio.fill('5150');
	await expect(answer).toHaveText('5150 shows as Portables, from the range 5100-5199.');
	await radio.fill('5200');
	await expect(answer).toHaveText('5200 has no alias: listeners see 5200.');
	await radio.fill('5001');
	await expect(answer).toHaveText('5001 shows as Engine 1: its own unit.');
	await radio.fill('51x');
	await expect(radio).toHaveAttribute('aria-invalid', 'true');
	await expect(page.locator('#which-radio-error')).toHaveText('Enter one radio ID, such as 5150.');

	// The search: an alias's part, and a radio ID finds the range covering it.
	const search = page.getByRole('searchbox', { name: 'Search' });
	await search.fill('portab');
	await expect(rowsOf(page)).toHaveCount(1);
	await expect(rowsOf(page).first()).toContainText('5100-5199');
	await search.fill('5150');
	await expect(rowsOf(page)).toHaveCount(1);
	await expect(rowsOf(page).first()).toContainText('Portables');
	await search.fill('engine 1');
	await expect(row(page, '5001')).toBeVisible();
	await page.getByRole('button', { name: 'Clear search and filter' }).click();
	await expect(search).toHaveValue('');
});

test('adding a radio and a range, the narrower rule shown, editing an alias in its row, and deleting after asking', async ({ page }) => {
	const b = base();
	const [one, first, last] = [b + 1, b + 100, b + 199];
	const range = `${first}-${last}`;
	await signIn(page, 'admin');
	await clean(page, b);
	try {
		await page.goto('/admin/units');
		await expect(count(page)).toHaveText(/^Showing \d+ of \d+ units?\.$/);
		const unit = page.getByRole('textbox', { name: 'Radio ID or range required' });
		const alias = page.getByRole('textbox', { name: 'Alias', exact: true });
		const addButton = page.getByRole('button', { name: 'Add the unit' });

		// Problems under their fields, the focus on them.
		await unit.fill(`${last}-${first}`);
		await addButton.click();
		await expect(unit).toHaveAttribute('aria-invalid', 'true');
		await expect(unit).toBeFocused();
		await expect(page.locator('#add-unit-error')).toHaveText(/a range whose last ID is above its first/);
		await unit.fill('5001');
		await addButton.click();
		await expect(page.locator('#add-unit-error')).toHaveText('5001 already has a unit: edit its alias in the table.');
		await unit.fill(String(one));
		await alias.fill('Engine; 1');
		await addButton.click();
		await expect(alias).toBeFocused();
		await expect(page.locator('#add-alias-error')).toHaveText('An alias can\'t hold ";".');

		// R26's two, added through the page.
		await alias.fill('Engine 1');
		await addButton.click();
		await expect(page.locator('p.status')).toHaveText(`Added ${one} Engine 1.`);
		await expect(unit).toHaveValue('');
		await unit.fill(` ${first} - ${last} `);
		await alias.fill('Portables');
		await addButton.click();
		await expect(page.locator('p.status')).toHaveText(`Added ${range} Portables.`);
		await expect(row(page, range)).toContainText('Portables');
		const units = (await (await page.request.get('/api/v1/admin/systems/1/units')).json()) as { from: number; to: number; alias: string }[];
		expect(units).toContainEqual(expect.objectContaining({ from: one, to: one, alias: 'Engine 1' }));
		expect(units).toContainEqual(expect.objectContaining({ from: first, to: last, alias: 'Portables' }));

		const radio = page.getByRole('textbox', { name: 'Radio ID', exact: true });
		await radio.fill(String(b + 150));
		await expect(page.locator('p.answer')).toHaveText(`${b + 150} shows as Portables, from the range ${range}.`);
		await radio.fill(String(b + 200));
		await expect(page.locator('p.answer')).toHaveText(`${b + 200} has no alias: listeners see ${b + 200}.`);

		// The alias edited in its row: Escape cancels, Enter saves; the focus back on Edit.
		const edit = page.getByRole('button', { name: `Edit the alias of ${range}` });
		await edit.click();
		const box = page.getByRole('textbox', { name: `Alias of ${range}` });
		await expect(box).toBeFocused();
		await expect(row(page, range)).toHaveAttribute('data-open', 'edit');
		await box.fill('Nothing');
		await box.press('Escape');
		await expect(page.locator('p.status')).toHaveText(`Nothing changed on ${range} Portables.`);
		await expect(edit).toBeFocused();
		await edit.click();
		await page.getByRole('textbox', { name: `Alias of ${range}` }).fill('Handhelds');
		await page.getByRole('textbox', { name: `Alias of ${range}` }).press('Enter');
		await expect(page.locator('p.status')).toHaveText(`Saved: ${range} is Handhelds now.`);
		await expect(row(page, range)).toContainText('Handhelds');
		await expect(page.getByRole('button', { name: `Edit the alias of ${range}` })).toBeFocused();
		await radio.fill(String(b + 150));
		await expect(page.locator('p.answer')).toHaveText(`${b + 150} shows as Handhelds, from the range ${range}.`);
		// No alias: its radio shows by its ID, even inside the range.
		await page.getByRole('button', { name: `Edit the alias of ${one}` }).click();
		await page.getByRole('textbox', { name: `Alias of ${one}` }).fill('');
		await page.getByRole('button', { name: `Save the alias of ${one}` }).click();
		await expect(page.locator('p.status')).toHaveText(`Saved: ${one} has no alias now.`);
		await expect(row(page, String(one))).toContainText('No alias');

		// Deleting asks first, in a dialog; cancelled, nothing changes.
		await page.getByRole('button', { name: `More actions: ${range} Handhelds` }).click();
		await page.getByRole('button', { name: 'Delete…' }).click();
		const dialog = page.getByRole('dialog', { name: `Delete ${range} Handhelds?` });
		await expect(dialog).toBeVisible();
		await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused();
		await dialog.getByRole('button', { name: 'Cancel' }).click();
		await expect(dialog).toHaveCount(0);
		await expect(row(page, range)).toBeVisible();
		await page.getByRole('button', { name: `More actions: ${range} Handhelds` }).click();
		await page.getByRole('button', { name: 'Delete…' }).click();
		await dialog.getByRole('button', { name: `Delete ${range} Handhelds` }).click();
		await expect(page.locator('p.status')).toHaveText(`Deleted ${range} Handhelds.`);
		await expect(row(page, range)).toHaveCount(0);
		await radio.fill(String(b + 150));
		await expect(page.locator('p.answer')).toHaveText(`${b + 150} has no alias: listeners see ${b + 150}.`);
		expect((await page.request.get(`/api/v1/admin/systems/1/units/${range}`)).status()).toBe(404);
	} finally {
		await clean(page, b);
	}
});

test('the Units page refuses signed out and a listener, as the admin API does', async ({ page }) => {
	await page.goto('/admin/units');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('This page is for admins');
	await signIn(page, 'listener');
	await page.goto('/admin/units');
	await expect(page.locator('main')).toHaveText('This page is for admins');
});

for (const look of looks) {
	for (const theme of themes) {
		for (const width of widths) {
			test(`axe: the Units page with an alias being edited and the delete dialog, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(90_000);
				await saveLook(context, baseURL!, look, theme);
				await page.setViewportSize({ width, height: 900 });
				await open(page);
				const tag = `${look}-${theme}-${width}`;
				await page.getByRole('textbox', { name: 'Radio ID', exact: true }).fill('5150');
				await expect(page.locator('p.answer')).toHaveText('5150 shows as Portables, from the range 5100-5199.');
				await axe(page, width);
				await shot(page, `units-${tag}`);
				// An alias being edited, with a problem under it.
				await page.getByRole('button', { name: 'Edit the alias of 5100-5199' }).click();
				await page.getByRole('textbox', { name: 'Alias of 5100-5199' }).fill('Bad; alias');
				await page.getByRole('button', { name: 'Save the alias of 5100-5199' }).click();
				await expect(page.locator('#edit-alias-error')).toHaveText('An alias can\'t hold ";".');
				await axe(page, width);
				await shot(page, `units-editing-${tag}`);
				await page.getByRole('button', { name: 'Cancel editing 5100-5199' }).click();
				// The add form's problems, More actions open, and the dialog.
				await page.getByRole('button', { name: 'Add the unit' }).click();
				await expect(page.locator('#add-unit-error')).toBeVisible();
				await page.getByRole('button', { name: 'More actions: 5001 Engine 1' }).click();
				await axe(page, width);
				await page.getByRole('button', { name: 'Delete…' }).click();
				await expect(page.getByRole('dialog', { name: 'Delete 5001 Engine 1?' })).toBeVisible();
				await axe(page, width);
				await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
			});
		}
	}
}
