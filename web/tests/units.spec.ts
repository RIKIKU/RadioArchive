/// <reference types="node" />
// Units in recent calls (issue #83; SPEC.md R26, R27, R32, R46, R61; A10;
// ADR 0019), against the binary with the fixture's units (5001 "Engine 1",
// 5100-5199 "Portables", 9003 "Covert radio", heard only on hidden 1003,
// and 9000-9009 "Covert fleet", whose radios are heard only where the
// public may not hear them) and its made-up archive
// (testing/webfixture/calls.go): 1002's calls carry 5001 with 5150, 5200
// (no alias), and 5001 alone, in turn. The unit filter suggests radios by
// alias or radio ID, a radio only a range names as "5150 in Portables",
// and range aliases by alias or radio IDs, only those heard on calls the
// public may hear; rows and the call card show each unit's alias, else its
// ID.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const SAVED = 'radioarchive.listening-set';

const list = (page: Page) => page.getByRole('list', { name: 'Recent calls' });
const rows = (page: Page) => list(page).getByRole('listitem');
const ids = (page: Page) => rows(page).evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.callId));
const unitBox = (page: Page) => page.getByRole('combobox', { name: 'Unit' });

async function withSet(page: Page, set: { id: number }[]) {
	await page.addInitScript(([k, s]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s })), [SAVED, set] as const);
}

const openFilters = async (page: Page) => {
	const toggle = page.getByRole('button', { name: 'Filters' });
	if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
};

/** The API's own answer for a search: its calls' IDs and their transmissions. */
async function apiCalls(page: Page, query: string): Promise<{ id: string; transmissions: { unit: number; alias: string | null }[] }[]> {
	return page.evaluate(async (q) => (await (await fetch(`/api/v1/recent-calls?${q}&limit=100`)).json()).calls, query);
}

/** Types q into the unit box and waits for unit search's answer to it. */
async function typeUnit(page: Page, q: string) {
	const answered = page.waitForResponse((r) => r.url().includes('/api/v1/units/search?q=') && decodeURIComponent(r.url()).endsWith(`q=${q.toLowerCase()}`));
	await unitBox(page).fill(q);
	await answered;
}

test('the unit filter: Engine 1 picked from unit search lists only the calls with 5001 in them', async ({ page }) => {
	await withSet(page, [{ id: 1002 }]);
	await page.goto('/recent');
	await expect(rows(page).first()).toBeVisible();
	await openFilters(page);
	await typeUnit(page, 'engine');
	const option = page.getByRole('option', { name: 'Engine 1 (5001)' });
	await expect(option).toBeVisible();
	await option.click();
	await expect(unitBox(page)).toHaveValue('Engine 1 (5001)');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(/\/recent\?unit=5001$/);
	await expect(page.getByText(/Unit Engine 1 \(5001\)/)).toBeVisible();

	const want = await apiCalls(page, 'talkgroups=1002&unit=5001');
	expect(want.length).toBeGreaterThan(0);
	for (const c of want) expect(c.transmissions.map((t) => t.unit)).toContain(5001);
	await expect(rows(page)).toHaveCount(want.length);
	expect(await ids(page)).toEqual(want.map((c) => c.id));
	for (const t of await rows(page).locator('.units').allInnerTexts()) expect(t).toContain('Engine 1');

	// The address alone opens the same list, the unit named by its alias.
	await page.goto('/recent?unit=5001');
	await expect(rows(page)).toHaveCount(want.length);
	await expect(page.getByText(/Unit Engine 1 \(5001\)/)).toBeVisible();
	await openFilters(page);
	await expect(unitBox(page)).toHaveValue('Engine 1 (5001)');

	// Typing a radio ID still works, and suggests it too.
	await typeUnit(page, '520');
	await expect(page.getByRole('option', { name: '5200', exact: true })).toBeVisible();
});

test('a range alias: Portables picked by its name lists the calls with any of its radios; a radio only it names is "5150 in Portables"', async ({ page }) => {
	await withSet(page, [{ id: 1002 }]);
	await page.goto('/recent');
	await expect(rows(page).first()).toBeVisible();
	await openFilters(page);
	await typeUnit(page, 'portables');
	const option = page.getByRole('option', { name: 'Portables (5100-5199)' });
	await expect(option).toBeVisible();
	await option.click();
	await expect(unitBox(page)).toHaveValue('Portables (5100-5199)');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(/\/recent\?unit=5100-5199$/);
	await expect(page.getByText(/Unit Portables \(5100-5199\)/)).toBeVisible();

	const want = await apiCalls(page, 'talkgroups=1002&unit=5100-5199');
	expect(want.length).toBeGreaterThan(0);
	for (const c of want) expect(c.transmissions.some((t) => t.unit >= 5100 && t.unit <= 5199)).toBe(true);
	await expect(rows(page)).toHaveCount(want.length);
	expect(await ids(page)).toEqual(want.map((c) => c.id));

	// The address alone opens the same list, the range named by its alias.
	await page.goto('/recent?unit=5100-5199');
	await expect(rows(page)).toHaveCount(want.length);
	await expect(page.getByText(/Unit Portables \(5100-5199\)/)).toBeVisible();
	await openFilters(page);
	await expect(unitBox(page)).toHaveValue('Portables (5100-5199)');

	// By its radio IDs, or the start of them, too.
	await typeUnit(page, '5100-');
	await expect(page.getByRole('option', { name: 'Portables (5100-5199)' })).toBeVisible();
	// A radio ID offers the radio, in its range, and never the range.
	await typeUnit(page, '515');
	await expect(page.getByRole('option', { name: '5150 in Portables' })).toBeVisible();
	await expect(page.getByRole('option', { name: /\(5100-5199\)/ })).toHaveCount(0);
	await page.getByRole('option', { name: '5150 in Portables' }).click();
	await expect(unitBox(page)).toHaveValue('5150 in Portables');
	await page.getByRole('button', { name: 'Show calls' }).click();
	await expect(page).toHaveURL(/\/recent\?unit=5150$/);
	await expect(page.getByText(/Unit 5150 in Portables/)).toBeVisible();
});

test("R26's ranges in the rows: 5001 is Engine 1, 5150 Portables, 5200 its ID", async ({ page }) => {
	await withSet(page, [{ id: 1002 }]);
	await page.goto('/recent?talkgroups=1002&order=oldest');
	const want = await apiCalls(page, 'talkgroups=1002&order=oldest');
	await expect(rows(page)).toHaveCount(want.length);
	const texts = await rows(page).locator('.units').allInnerTexts();
	expect(texts).toContain('Units Engine 1, Portables');
	expect(texts).toContain('Unit 5200');
	expect(texts).toContain('Unit Engine 1');
	// As the API names them.
	const both = want.find((c) => c.transmissions.some((t) => t.unit === 5150))!;
	expect(both.transmissions.map((t) => t.alias)).toEqual(['Engine 1', 'Portables']);
	expect(want.find((c) => c.transmissions.some((t) => t.unit === 5200))!.transmissions[0].alias).toBeNull();
});

test("the call card's units heard by alias", async ({ page }) => {
	await page.goto('/');
	const c = (await apiCalls(page, 'unit=5150'))[0];
	await page.goto(`/calls/${c.id}`);
	await expect(page.locator('.card .units')).toHaveText('Engine 1, Portables');
});

test('units heard only on hidden 1003 are never suggested, by alias or ID, nor a range whose radios are heard only there', async ({ page }) => {
	await page.goto('/recent');
	await openFilters(page);
	for (const q of ['covert', 'Covert radio', '9003', '900', 'Covert fleet', 'fleet', '9000-9009', '9000-']) {
		const body = page.waitForResponse((r) => r.url().includes('/api/v1/units/search?q=') && decodeURIComponent(r.url()).endsWith(`q=${q.toLowerCase()}`));
		await unitBox(page).fill(q);
		expect(await (await body).json()).toEqual({ units: [] });
		await expect(page.getByRole('listbox', { name: 'Units', includeHidden: true }).getByRole('option')).toHaveCount(0);
	}
	const text = await page.locator('body').innerText();
	for (const s of ['Covert', '9003', '1003', 'Admin']) expect(text, s).not.toContain(s);
});

test('axe: the unit filter suggesting, aliases in the list and on the card, in both looks and both themes at 375 px and 1,280 px, and the screenshots', async ({ page }, info) => {
	test.setTimeout(4 * 60_000);
	const shots = process.env.RA_SHOTS && info.project.name === 'chromium' ? process.env.RA_SHOTS : '';
	await withSet(page, [{ id: 1002 }]);
	const check = async (state: string, ready: () => Promise<void>) => {
		for (const width of [1280, 375]) {
			await page.setViewportSize({ width, height: width === 375 ? 812 : 900 });
			for (const look of ['harbour', 'console'])
				for (const theme of ['light', 'dark']) {
					await page.evaluate(
						([l, t]) => {
							const d = document.documentElement.dataset;
							if (l === 'console') d.look = l;
							else delete d.look;
							d.theme = t;
						},
						[look, theme]
					);
					await ready();
					const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
					expect(results.violations, `${state}, ${look} ${theme}, ${width} px`).toEqual([]);
					expect(await page.evaluate(() => document.documentElement.scrollWidth), `${state} at ${width} px scrolls sideways`).toBeLessThanOrEqual(width);
					if (shots) await page.screenshot({ path: `${shots}/units-${state}-${look}-${theme}-${width}.png` });
				}
		}
	};
	await page.goto('/recent');
	await openFilters(page);
	await typeUnit(page, 'e');
	await check('filter', async () => {
		if (!(await page.getByRole('option', { name: 'Engine 1 (5001)' }).isVisible())) await unitBox(page).click();
		await expect(page.getByRole('option', { name: 'Engine 1 (5001)' })).toBeVisible();
	});
	// A range alias suggested by its name, and a radio only a range names
	// by its ID (ADR 0019).
	await typeUnit(page, 'port');
	await check('range', async () => {
		if (!(await page.getByRole('option', { name: 'Portables (5100-5199)' }).isVisible())) await unitBox(page).click();
		await expect(page.getByRole('option', { name: 'Portables (5100-5199)' })).toBeVisible();
	});
	await typeUnit(page, '5');
	await check('in-range', async () => {
		if (!(await page.getByRole('option', { name: '5150 in Portables' }).isVisible())) await unitBox(page).click();
		await expect(page.getByRole('option', { name: '5150 in Portables' })).toBeVisible();
	});
	await page.goto('/recent?talkgroups=1002');
	await check('list', () => expect(rows(page).filter({ hasText: 'Portables' }).first()).toBeVisible());
	const c = (await apiCalls(page, 'unit=5150'))[0];
	await page.goto(`/calls/${c.id}`);
	await check('card', () => expect(page.locator('.card .units')).toHaveText('Engine 1, Portables'));
});
