/// <reference types="node" />
// The Listen page's tiles (SPEC.md R42; A39; ADR 0048) on a made-up
// catalogue bigger than the test fixture's, served in place of the API's:
// two systems, groups and tags of several talkgroups, so a tile can be
// partly on, and 1,200 talkgroups, as a real deployment has, for All on.
// The fixture's own catalogue is listening-set.spec.ts's.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const SAVED = 'radioarchive.listening-set';

function talkgroup(system: number, id: number, group: string, tag: string) {
	return { system, id, label: `TG${id}`, name: `Channel ${id}`, tag, groups: [group], led_colour: null, alert: null, delay_minutes: 0, fireground_channel: null };
}

// System 1: Harbour (3 talkgroups: 2 dispatch, 1 fireground) and Ridge (1,
// fireground). System 2: 1,196 in Valley, every one fireground.
const catalogue = {
	systems: [
		{ id: 1, label: 'EX', name: 'Example network' },
		{ id: 2, label: 'WD', name: 'Wide network' }
	],
	groups: ['Harbour', 'Ridge', 'Valley'],
	tags: ['dispatch', 'fireground'],
	talkgroups: [
		talkgroup(1, 101, 'Harbour', 'dispatch'),
		talkgroup(1, 102, 'Harbour', 'dispatch'),
		talkgroup(1, 103, 'Harbour', 'fireground'),
		talkgroup(1, 201, 'Ridge', 'fireground'),
		...Array.from({ length: 1196 }, (_, i) => talkgroup(2, 3000 + i, 'Valley', 'fireground'))
	]
};

async function open(page: Page, set: { system: number; id: number }[] = []) {
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
	await page.addInitScript(([k, s]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s })), [SAVED, set] as const);
	await page.goto('/');
	await expect(summary(page)).toBeVisible();
}

const summary = (page: Page) => page.getByRole('status').filter({ hasText: 'talkgroups on' });
const tile = (page: Page, list: string, name: string) => page.getByRole('list', { name: list }).getByRole('button', { name: new RegExp(`^${name}`) });
const saved = (page: Page) => page.evaluate((k) => JSON.parse(localStorage.getItem(k) ?? 'null')?.talkgroups, SAVED);

test('A39: each tile shows all on, partly on or off by its mark, its words and aria-pressed; a tap on a partly-on tile switches all of it on', async ({ page }) => {
	await open(page, [{ system: 1, id: 101 }]);
	await expect(summary(page)).toHaveText('1 of 1,200 talkgroups on');
	// Per system: a heading, then Agencies and Categories.
	await expect(page.getByRole('heading', { name: 'Example network' })).toBeVisible();
	await expect(page.getByRole('heading', { name: 'Wide network' })).toBeVisible();

	const harbour = tile(page, 'Agencies in Example network', 'Harbour');
	const dispatch = tile(page, 'Categories in Example network', 'dispatch');
	const ridge = tile(page, 'Agencies in Example network', 'Ridge');
	await expect(harbour).toHaveAttribute('aria-pressed', 'mixed');
	await expect(harbour).toContainText('1 of 3 on');
	await expect(harbour).toHaveAttribute('data-state', 'mixed');
	await expect(dispatch).toHaveAttribute('aria-pressed', 'mixed');
	await expect(dispatch).toContainText('1 of 2 on');
	await expect(ridge).toHaveAttribute('aria-pressed', 'false');
	await expect(ridge).toContainText('1 off');
	// What's on: the agencies with anything on, fully or partly.
	await expect(page.locator('.whats-on')).toContainText(/Harbour\s*\(Example network\)\s*1\/3/);

	// Partly on: the tap switches the rest on.
	await dispatch.click();
	await expect(dispatch).toHaveAttribute('aria-pressed', 'true');
	await expect(dispatch).toContainText('All 2 on');
	await expect(harbour).toContainText('2 of 3 on');
	await harbour.click();
	await expect(harbour).toHaveAttribute('aria-pressed', 'true');
	await expect(harbour).toContainText('All 3 on');
	// All on: the tap switches it all off, and the tag's tile with it.
	await harbour.click();
	await expect(harbour).toHaveAttribute('aria-pressed', 'false');
	await expect(dispatch).toHaveAttribute('aria-pressed', 'false');
	await expect(summary(page)).toHaveText('0 of 1,200 talkgroups on');
	expect((await saved(page)) ?? [], 'an empty set').toEqual([]);

	// Every talkgroup isn't a button: single ones come from the search.
	await expect(page.getByRole('button', { name: /^Channel / })).toHaveCount(0);
	await page.getByLabel('Search talkgroups').fill('TG201');
	await page.getByRole('checkbox', { name: /^Channel 201/ }).check();
	await expect(ridge).toHaveAttribute('aria-pressed', 'true');
});

test('A39: All on and All off with 1,200 talkgroups stay quick', async ({ page }) => {
	await open(page);
	const started = Date.now();
	await page.getByRole('button', { name: 'All on' }).click();
	await expect(summary(page)).toHaveText('1,200 of 1,200 talkgroups on');
	const on = Date.now() - started;
	await expect(tile(page, 'Agencies in Wide network', 'Valley')).toContainText('All 1,196 on');
	expect((await saved(page)).length).toBe(1200);
	await page.getByRole('button', { name: 'All off' }).click();
	await expect(summary(page)).toHaveText('0 of 1,200 talkgroups on');
	console.log(`A39: All on took ${on} ms to show`);
	expect(on, 'All on shows within 2 s').toBeLessThan(2_000);
});

for (const width of [375, 1280] as const)
	test(`A36: axe on the tiles, partly on, in both looks and both themes, ${width}px`, async ({ page }) => {
		await page.setViewportSize({ width, height: 900 });
		await open(page, [{ system: 1, id: 101 }, { system: 1, id: 201 }]);
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
				expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
				const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
				expect(results.violations, `${look} ${theme}`).toEqual([]);
			}
	});
