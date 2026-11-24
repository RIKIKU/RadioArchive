/// <reference types="node" />
// The app shell in the binary (issue #54): the Listen page, the look and
// theme on first paint (R82), R74 for the page, and axe-core in both looks
// and both themes at phone and desktop widths (the start of A36).
import { test, expect, type BrowserContext } from '@playwright/test';
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

test('the Listen page opens at / with Start listening', async ({ page }) => {
	await page.goto('/');
	await expect(page).toHaveTitle('Listen · RadioArchive');
	await expect(page.getByRole('heading', { level: 1, name: 'Listen' })).toBeVisible();
	await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
	// Any other path outside the API is the app's too.
	await page.goto('/somewhere/else');
	await expect(page.getByRole('link', { name: 'RadioArchive' })).toBeVisible();
});

test('R82: the page is sent in the saved look and theme, Harbour by default', async ({ page, context, request }) => {
	// Sent as built: Harbour, following the system.
	const plain = await (await request.get('/')).text();
	expect(plain).toContain('<html lang="en-AU">');

	// Chosen in the frame, saved, and sent that way next time.
	await page.goto('/');
	await page.getByTestId('look').selectOption('console');
	await page.getByTestId('theme').selectOption('dark');
	await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
	await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
	const cookies = Object.fromEntries((await context.cookies()).map((c) => [c.name, c.value]));
	expect(cookies).toMatchObject({ look: 'console', theme: 'dark' });
	const sent = await page.request.get('/', { headers: { Cookie: `look=${cookies.look}; theme=${cookies.theme}` } });
	expect(await sent.text()).toContain('<html data-look="console" data-theme="dark" lang="en-AU">');
	await page.reload();
	await expect(page.locator('html')).toHaveAttribute('data-look', 'console');
	await expect(page.getByTestId('look')).toHaveValue('console');
	await expect(page.getByTestId('theme')).toHaveValue('dark');

	// Back to Harbour and the system's theme.
	await page.getByTestId('look').selectOption('harbour');
	await page.getByTestId('theme').selectOption('system');
	await expect(page.locator('html')).not.toHaveAttribute('data-look', /./);
	await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
	await page.reload();
	await expect(page.locator('html')).not.toHaveAttribute('data-look', /./);
	await expect(page.getByTestId('theme')).toHaveValue('system');
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

for (const look of looks)
	for (const theme of themes)
		for (const width of widths)
			test(`axe: the Listen page, ${look}, ${theme}, ${width}px`, async ({ page, context, baseURL }) => {
				await save(context, baseURL!, look, theme);
				await page.setViewportSize({ width, height: 800 });
				await page.goto('/');
				await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
				const html = page.locator('html');
				if (look === 'console') await expect(html).toHaveAttribute('data-look', 'console');
				else await expect(html).not.toHaveAttribute('data-look', /./);
				await expect(html).toHaveAttribute('data-theme', theme);
				// Nothing wider than the screen.
				expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
				expect(results.violations).toEqual([]);
				// The pull request's screenshots (RA_SHOTS names a folder).
				if (process.env.RA_SHOTS && test.info().project.name === 'chromium')
					await page.screenshot({ path: `${process.env.RA_SHOTS}/listen-${look}-${theme}-${width}.png`, fullPage: true });
			});
