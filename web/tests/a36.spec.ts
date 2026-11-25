// SPEC.md A36 in one sweep (issue #155): every page in the menu (R84), the
// listener's and the admin's, and the admin pages' sign-in prompt, passes
// axe-core in both looks and both themes at 375 px and 1,280 px, and every
// required field on them carries ArchiveUx's Required tag in its label.
// Each page's own tests check its states (dialogs, errors, notices); this
// one makes sure no page is left out.
import { expect, test, type Page } from '@playwright/test';
import { axe, inLook, looks, ownLook, saveLook, signIn, themes, widths } from './admin.ts';

const listener = ['/', '/recent', '/basket', '/conversation'];
const admin = ['/admin/talkgroups', '/admin/units', '/admin/systems', '/admin/recorders', '/admin/accounts', '/admin/audit', '/admin/settings'];

/** Every visible required field's label holds the Required tag (ArchiveUx section 5). */
async function requiredTagged(page: Page, path: string) {
	const missing = await page.evaluate(() =>
		[...document.querySelectorAll<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>('input[required], select[required], textarea[required]')]
			.filter((el) => el.offsetParent !== null)
			.filter((el) => {
				const label = (el.id && document.querySelector(`label[for="${el.id}"]`)) || el.closest('label');
				return !label?.querySelector('.required');
			})
			.map((el) => el.name || el.id || el.outerHTML.slice(0, 60))
	);
	expect(missing, `required fields without the Required tag on ${path}`).toEqual([]);
}

async function settled(page: Page) {
	await expect(page.getByRole('heading', { level: 1 })).toBeVisible({ timeout: 15_000 });
	await expect(page.getByRole('status').filter({ hasText: /^Loading/ })).toHaveCount(0, { timeout: 15_000 });
}

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`A36: every page, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				test.setTimeout(600_000);
				await saveLook(context, baseURL!, look, theme);
				await page.setViewportSize({ width, height: 900 });
				// Signed out: the listener's pages, and an admin page's sign-in prompt.
				for (const path of [...listener, '/admin/settings']) {
					await page.goto(path);
					await settled(page);
					await inLook(page, look, theme);
					await axe(page, width);
					await requiredTagged(page, path);
				}
				// Signed in as an admin: the admin pages, and the listener's again.
				await ownLook(page);
				await signIn(page, 'admin');
				for (const path of [...admin, ...listener]) {
					await page.goto(path);
					await settled(page);
					await inLook(page, look, theme);
					await axe(page, width);
					await requiredTagged(page, path);
				}
			});
