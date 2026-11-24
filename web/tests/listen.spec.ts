/// <reference types="node" />
// The app shell in the binary (issues #54, #59): the Listen page in
// ArchiveUx's frame, the look and theme on first paint (R82), R74 for the
// page, and axe-core on the Listen page and its picker in both looks and
// both themes at phone and desktop widths (A36 for this page).
import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const looks = ['harbour', 'console'] as const;
const themes = ['light', 'dark'] as const;
const widths = [375, 1280] as const;

async function save(context: BrowserContext, baseURL: string, look: string, theme: string) {
	const url = new URL(baseURL).origin;
	await context.addCookies([
		{ name: 'look', value: look, url },
		{ name: 'theme', value: theme, url }
	]);
}

/** Opens the account menu, where the look and theme are chosen. */
async function accountMenu(page: Page) {
	const button = page.getByRole('button', { name: 'Account menu, signed out' });
	if ((await button.getAttribute('aria-expanded')) !== 'true') await button.click();
}

test("the Listen page opens at / with Start listening, in ArchiveUx's frame", async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveTitle('Listen · RadioArchive');
	await expect(page.getByRole('heading', { level: 1, name: 'Listen' })).toBeVisible();
	// Start listening is in the frame's dock (R44, R81), on every page.
	await expect(page.getByRole('region', { name: 'Now playing' }).getByRole('button', { name: 'Start listening' })).toBeVisible();
	// The side menu, with the brand and Listen as the current page.
	const menu = page.locator('nav.side');
	await expect(menu.getByRole('link', { name: 'RadioArchive' })).toBeVisible();
	await expect(menu.getByRole('link', { name: 'Listen' })).toHaveAttribute('aria-current', 'page');
	// Signed out: Sign in and the playback settings, off by default; no app switcher.
	await accountMenu(page);
	await expect(page.getByText('Not signed in')).toBeVisible();
	await expect(page.getByRole('link', { name: 'Sign in' })).toBeVisible();
	await expect(page.getByRole('checkbox', { name: /Alert sounds/ })).not.toBeChecked();
	await expect(page.getByRole('link', { name: /^Switch to/ })).toHaveCount(0);
	// Nothing in the dock before Start listening but the button.
	await expect(page.locator('section.dock')).toHaveCount(1);
	await expect(page.locator('section.dock').getByRole('button')).toHaveCount(1);
	// Any other path outside the API is the app's too.
	await page.goto('/somewhere/else');
	await expect(page.getByRole('link', { name: 'RadioArchive' })).toBeVisible();
});

test('R82: the page is sent in the saved look and theme, Harbour by default', async ({ page, context, request }) => {
	// Sent as built: Harbour, following the system.
	const plain = await (await request.get('/')).text();
	expect(plain).toContain('<html lang="en-AU">');

	// Chosen in the account menu, signed out, saved, and sent that way next time.
	await page.goto('/');
	await accountMenu(page);
	await page.getByRole('combobox', { name: 'Look' }).selectOption('console');
	await page.getByRole('combobox', { name: 'Colour theme' }).selectOption('dark');
	await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
	await expect
		.poll(async () => Object.fromEntries((await context.cookies()).map((c) => [c.name, c.value])))
		.toMatchObject({ look: 'console', theme: 'dark' });
	const sent = await page.request.get('/', { headers: { Cookie: 'look=console; theme=dark' } });
	expect(await sent.text()).toContain('<html data-look="console" data-theme="dark" lang="en-AU">');
	await page.reload();
	await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
	await expect(page.getByRole('combobox', { name: 'Look', includeHidden: true })).toHaveValue('console');
	await expect(page.getByRole('combobox', { name: 'Colour theme', includeHidden: true })).toHaveValue('dark');

	// Back to Harbour and the system's theme.
	await accountMenu(page);
	await page.getByRole('combobox', { name: 'Look' }).selectOption('harbour');
	await page.getByRole('combobox', { name: 'Colour theme' }).selectOption('system');
	await expect(page.locator('html')).not.toHaveAttribute('data-look', /./);
	await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
	await page.reload();
	await expect(page.locator('html')).not.toHaveAttribute('data-look', /./);
	await expect(page.getByRole('combobox', { name: 'Colour theme', includeHidden: true })).toHaveValue('system');
});

test('R74: robots.txt keeps crawlers out, and nothing is indexed', async ({ request }) => {
	const robots = await request.get('/robots.txt');
	expect(robots.status()).toBe(200);
	expect(await robots.text()).toBe('User-agent: *\nDisallow: /\n');
	for (const path of ['/', '/robots.txt', '/healthz', '/api/v1/nothing']) {
		expect((await request.get(path)).headers()['x-robots-tag'], path).toBe('noindex');
	}
	// The app never answers for the API.
	const api = await request.get('/api/v1/nothing');
	expect(api.status()).toBe(404);
	expect(api.headers()['content-type'] ?? '').not.toContain('text/html');
});

async function axe(page: Page, width: number) {
	// Nothing wider than the screen.
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
}

/** The pull request's screenshots (RA_SHOTS names a folder). */
async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS || test.info().project.name !== 'chromium') return;
	// The whole page in one screen, so the frame's sticky and fixed parts
	// sit where they would, and nothing hovered.
	const size = page.viewportSize()!;
	await page.mouse.move(0, 0);
	await page.evaluate(() => window.scrollTo(0, 0));
	const height = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: size.width, height: Math.max(height, size.height) });
	await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}.png` });
	await page.setViewportSize(size);
}

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the Listen page and the picker, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				await save(context, baseURL!, look, theme);
				await page.setViewportSize({ width, height: 900 });
				await page.goto('/');
				const html = page.locator('html');
				if (look === 'console') await expect(html).toHaveAttribute('data-look', 'console');
				else await expect(html).not.toHaveAttribute('data-look', /./);
				await expect(html).toHaveAttribute('data-theme', theme);
				// The empty page.
				await expect(page.getByText('Your listening set is empty.')).toBeVisible();
				await axe(page, width);

				// A listener with {1001}, 1001 priority, opens a fireground link
				// whose number names no public talkgroup.
				await page.evaluate(() =>
					localStorage.setItem('radioarchive.listening-set', JSON.stringify({ v: 1, talkgroups: [{ id: 1001, priority: true }] }))
				);
				await page.goto('/?fireground=99');
				await expect(page.getByText("This channel isn't available.")).toBeVisible();
				await axe(page, width);
				await shot(page, `unavailable-${look}-${theme}-${width}`);

				// Then one that does: the set, the session's addition, and the
				// picker with a group open.
				await page.goto('/?fireground=41');
				await expect(page.getByText('is added for this session')).toBeVisible();
				await page.getByRole('button', { name: /^North/ }).click();
				await expect(page.getByRole('list', { name: 'North' }).getByRole('listitem')).toHaveCount(1);
				await axe(page, width);
				await shot(page, `listen-${look}-${theme}-${width}`);

				// The account menu, open.
				await accountMenu(page);
				await axe(page, width);
				await page.keyboard.press('Escape');

				// A share link's offer.
				await page.goto('/?set=1001,1002p');
				await expect(page.getByRole('heading', { name: 'Open this shared listening set?' })).toBeVisible();
				await axe(page, width);
				await shot(page, `share-${look}-${theme}-${width}`);

				// A search.
				await page.getByRole('button', { name: 'Keep mine' }).click();
				await page.getByLabel('Search talkgroups').fill('fire');
				await expect(page.getByRole('list', { name: 'Search results' }).getByRole('listitem')).toHaveCount(1);
				await axe(page, width);
			});
