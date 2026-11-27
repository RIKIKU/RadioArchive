/// <reference types="node" />
// The Listen page's tiles (SPEC.md R42; A39; ADR 0048) on a made-up
// catalogue bigger than the test fixture's, served in place of the API's:
// two systems, groups and tags of several talkgroups, so a tile can be
// partly on, and 1,200 talkgroups, as a real deployment has, for All on.
// The fixture's own catalogue is listening-set.spec.ts's.
import { test, expect, type Locator, type Page } from '@playwright/test';
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

async function open(page: Page, set: { system: number; id: number; priority?: boolean }[] = []) {
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
	// Once per tab, so a reload keeps what the test changed.
	await page.addInitScript(
		([k, s]) => {
			if (sessionStorage.getItem('seeded')) return;
			sessionStorage.setItem('seeded', '1');
			localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s }));
		},
		[SAVED, set] as const
	);
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

	// Every talkgroup isn't a tile: single ones come from the search.
	await expect(page.getByRole('list', { name: /^(Agencies|Categories) in / }).getByRole('button', { name: /^Channel / })).toHaveCount(0);
	await page.getByLabel('Search talkgroups').fill('TG201');
	await chip(results(page), 'Channel 201').click();
	await expect(ridge).toHaveAttribute('aria-pressed', 'true');
});

// The chips (SPEC.md R112; ADR 0051): a tap switches one on or off, a long
// press or the star flips priority, a press that wanders does nothing.

const results = (page: Page) => page.getByRole('list', { name: 'Search results' });
const onList = (page: Page) => page.getByRole('region', { name: /^Talkgroups on/ });
const chip = (scope: Page | Locator, name: string) => scope.getByRole('button', { name: new RegExp(`^${name} `) });
const star = (scope: Page | Locator, name: string) => scope.getByRole('button', { name: `Priority: ${name}` });
const priorityOf = (page: Page, id: number) =>
	page.evaluate(([k, i]) => JSON.parse(localStorage.getItem(k as string) ?? 'null')?.talkgroups?.find((t: { id: number }) => t.id === i)?.priority ?? false, [SAVED, id] as const);

/** A press of ms held at the chip's middle, with the mouse, moving by drift px first. */
async function press(page: Page, target: Locator, ms: number, drift = 0) {
	// In the middle of the screen: not under the sticky display or the dock.
	await target.evaluate((e) => e.scrollIntoView({ block: 'center' }));
	const b = (await target.boundingBox())!;
	await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2);
	await page.mouse.down();
	if (drift) await page.mouse.move(b.x + b.width / 2 + drift, b.y + b.height / 2, { steps: 5 });
	await page.waitForTimeout(ms);
	await page.mouse.up();
}

/** A touch press of ms by Chromium's own touch input (CDP), so the browser makes its own click, or a scroll when it wanders. */
async function touch(page: Page, target: Locator, ms: number, drift = 0) {
	const cdp = await page.context().newCDPSession(page);
	// In the middle of the screen: not under the sticky display or the dock.
	await target.evaluate((e) => e.scrollIntoView({ block: 'center' }));
	const b = (await target.boundingBox())!;
	const at = { x: b.x + b.width / 2, y: b.y + b.height / 2 };
	await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [at] });
	if (drift) for (let i = 1; i <= 5; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: at.x, y: at.y + (drift * i) / 5 }] });
	await page.waitForTimeout(ms);
	await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
	await cdp.detach();
}

test('R112: a tap switches a chip on and off; it stays where it was, off, until the page is left', async ({ page }) => {
	await open(page, [{ system: 1, id: 101 }, { system: 1, id: 102 }]);
	await page.getByRole('button', { name: /^Harbour \(Example network\) — 2 on/ }).click();
	const c = chip(onList(page), 'Channel 101');
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	await c.click();
	await expect(c).toHaveAttribute('aria-pressed', 'false');
	await expect(c).toContainText('Off');
	expect((await saved(page)).map((t: { id: number }) => t.id)).toEqual([102]);
	await expect(page.getByRole('button', { name: /^Harbour \(Example network\) — 1 on/ })).toBeVisible();
	// A second tap puts it back.
	await c.click();
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	expect((await saved(page)).map((t: { id: number }) => t.id).sort()).toEqual([101, 102]);
	// Off, then the page left and come back to: gone from the talkgroups on.
	await c.click();
	await page.reload();
	await page.getByRole('button', { name: /^Harbour \(Example network\) — 1 on/ }).click();
	await expect(chip(onList(page), 'Channel 101')).toHaveCount(0);
	await expect(chip(onList(page), 'Channel 102')).toHaveAttribute('aria-pressed', 'true');
});

test('R112: a long press with the mouse flips priority and leaves the chip on; a press that wanders does nothing', async ({ page }) => {
	await open(page, [{ system: 1, id: 101 }]);
	await page.getByLabel('Search talkgroups').fill('TG101');
	const c = chip(results(page), 'Channel 101');
	await press(page, c, 700);
	await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	expect(await priorityOf(page, 101)).toBe(true);
	// Again: priority off, still on.
	await press(page, c, 700);
	await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'false');
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	// A click is a tap.
	await c.click();
	await expect(c).toHaveAttribute('aria-pressed', 'false');
	await c.click();
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	// A press that wanders 30 px, short or long, switches nothing and marks nothing.
	for (const ms of [50, 700]) {
		await press(page, c, ms, 30);
		await expect(c).toHaveAttribute('aria-pressed', 'true');
		await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'false');
	}
	// A long press on one that's off switches it on as priority (ADR 0051).
	await press(page, chip(results(page), 'Channel 102'), 700);
	await expect(chip(results(page), 'Channel 102')).toHaveAttribute('aria-pressed', 'true');
	expect(await priorityOf(page, 102)).toBe(true);
});

test.describe('on a touch screen', () => {
	test.use({ hasTouch: true, viewport: { width: 375, height: 812 } });

	test('R112: a touch press of 500 ms flips priority and nothing else; one that drifts marks and switches nothing; a tap switches', async ({ page }, info) => {
		await open(page, [{ system: 1, id: 101 }]);
		await page.getByLabel('Search talkgroups').fill('TG101');
		const c = chip(results(page), 'Channel 101');
		// The chip keeps panning and drops the double-tap zoom's wait.
		expect(await c.evaluate((e) => getComputedStyle(e).touchAction)).toBe('manipulation');
		// A tap.
		await c.tap();
		await expect(c).toHaveAttribute('aria-pressed', 'false');
		await c.tap();
		await expect(c).toHaveAttribute('aria-pressed', 'true');
		if (info.project.name === 'chromium') {
			// The browser's own touch: a long press, then one that drifts.
			await touch(page, c, 700);
			await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
			await expect(c).toHaveAttribute('aria-pressed', 'true');
			await touch(page, c, 700, 30);
			await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
			await expect(c).toHaveAttribute('aria-pressed', 'true');
		}
		// Touch pointer events, as every browser sends them: a hold, a drift, and a scroll's pointercancel.
		const pointer = (types: string[], dy = 0, wait = 0) =>
			c.evaluate(
				async (el, [types, dy, wait]) => {
					const r = el.getBoundingClientRect();
					const at = { clientX: r.x + r.width / 2, clientY: r.y + r.height / 2 };
					for (const t of types as string[]) {
						if (t === 'wait') await new Promise((ok) => setTimeout(ok, wait as number));
						else
							el.dispatchEvent(
								new PointerEvent(t, { bubbles: true, pointerType: 'touch', isPrimary: true, button: 0, clientX: at.clientX, clientY: at.clientY + (t === 'pointermove' ? (dy as number) : 0) })
							);
					}
				},
				[types, dy, wait] as const
			);
		const before = await star(results(page), 'Channel 101').getAttribute('aria-pressed');
		const flipped = before === 'true' ? 'false' : 'true';
		await pointer(['pointerdown', 'wait', 'pointerup'], 0, 700);
		await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', flipped);
		await pointer(['pointerdown', 'pointermove', 'wait', 'pointerup'], 30, 700);
		await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', flipped);
		await pointer(['pointerdown', 'pointercancel', 'wait', 'pointerup'], 0, 700);
		await expect(star(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', flipped);
		await expect(c).toHaveAttribute('aria-pressed', 'true');
		// No context menu, text selection or callout on a long press.
		expect(await c.evaluate((e) => {
			const s = getComputedStyle(e.parentElement!) as CSSStyleDeclaration & { webkitUserSelect?: string };
			return s.userSelect || s.webkitUserSelect;
		})).toBe('none');
	});
});

test('R112: the star is a toggle button for the keyboard: Space and Enter flip priority; on the chip they switch it', async ({ page }) => {
	await open(page, [{ system: 1, id: 101 }]);
	await page.getByLabel('Search talkgroups').fill('TG101');
	const s = star(results(page), 'Channel 101');
	await s.focus();
	await page.keyboard.press('Space');
	await expect(s).toHaveAttribute('aria-pressed', 'true');
	expect(await priorityOf(page, 101)).toBe(true);
	await page.keyboard.press('Enter');
	await expect(s).toHaveAttribute('aria-pressed', 'false');
	expect(await priorityOf(page, 101)).toBe(false);
	// The chip, a Tab back: Space and Enter switch it, never mark it.
	await page.keyboard.press('Shift+Tab');
	const c = chip(results(page), 'Channel 101');
	await expect(c).toBeFocused();
	await page.keyboard.press('Space');
	await expect(c).toHaveAttribute('aria-pressed', 'false');
	await page.keyboard.press('Enter');
	await expect(c).toHaveAttribute('aria-pressed', 'true');
	await expect(s).toHaveAttribute('aria-pressed', 'false');
});

test('R112: the talkgroups on: priority first, then by agency per system, each folded until opened', async ({ page }) => {
	await open(page, [
		{ system: 1, id: 101, priority: true },
		{ system: 1, id: 103 },
		{ system: 1, id: 201 },
		{ system: 2, id: 3000 },
		{ system: 2, id: 3001 }
	]);
	await expect(page.getByRole('heading', { name: 'Talkgroups on (5)' })).toBeVisible();
	// Priority first, as chips.
	const prio = page.getByRole('list', { name: 'Priority talkgroups' });
	await expect(prio.getByRole('listitem')).toHaveText([/^ *Channel 101/]);
	await expect(star(prio, 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
	// Then by agency, the system named as there are two, every one folded, its chips not drawn.
	const heads = page.getByRole('list', { name: 'Talkgroups on by agency' }).getByRole('button');
	await expect(heads).toHaveText([/Harbour \(Example network\)\s*— 2 on/, /Ridge \(Example network\)\s*— 1 on/, /Valley \(Wide network\)\s*— 2 on/]);
	for (const h of await heads.all()) await expect(h).toHaveAttribute('aria-expanded', 'false');
	await expect(onList(page).locator('.groups .chip')).toHaveCount(0);
	// Opened: its chips, the priority one starred there too.
	await heads.first().click();
	await expect(heads.first()).toHaveAttribute('aria-expanded', 'true');
	const harbour = page.getByRole('list', { name: 'Harbour (Example network)' });
	await expect(harbour.getByRole('listitem')).toHaveText([/^ *Channel 101/, /^ *Channel 103/]);
	await expect(star(harbour, 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
	await expect(star(harbour, 'Channel 103')).toHaveAttribute('aria-pressed', 'false');
	// A star taken off leaves it in Priority, unstarred, until the page is left.
	await star(harbour, 'Channel 101').click();
	await expect(prio.getByRole('listitem')).toHaveText([/^ *Channel 101/]);
	await expect(star(prio, 'Channel 101')).toHaveAttribute('aria-pressed', 'false');
	await page.reload();
	await expect(page.getByText('None yet. Hold a talkgroup below, or tap its star, to make it priority.')).toBeVisible();
	// Folded again.
	await expect(page.getByRole('list', { name: 'Talkgroups on by agency' }).getByRole('button').first()).toHaveAttribute('aria-expanded', 'false');
});

test('R112: the search finds a talkgroup by its number first, on or off, and hides the talkgroups on while it has text', async ({ page }) => {
	// 101's label is "102", which names it exactly too: 102, the number, still comes first.
	const tricky = { ...catalogue, talkgroups: catalogue.talkgroups.map((t) => (t.id === 101 ? { ...t, label: '102' } : t)) };
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: tricky }));
	await page.addInitScript(([k]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: [{ system: 1, id: 101 }] })), [SAVED] as const);
	await page.goto('/');
	await expect(onList(page)).toBeVisible();
	const search = page.getByLabel('Search talkgroups');
	// Under the box, above the tiles.
	const [box, field, tiles] = [await summary(page).boundingBox(), await search.boundingBox(), await page.getByRole('list', { name: 'Agencies in Example network' }).boundingBox()];
	expect(field!.y).toBeGreaterThan(box!.y);
	expect(field!.y).toBeLessThan(tiles!.y);
	await search.fill('102');
	await expect(onList(page)).toBeHidden();
	await expect(results(page).getByRole('listitem').first()).toHaveText(/^ *Channel 102/);
	await expect(results(page).getByRole('listitem').nth(1)).toHaveText(/^ *Channel 101/);
	// On or off, both are there; the one on says so.
	await expect(chip(results(page), 'Channel 101')).toHaveAttribute('aria-pressed', 'true');
	await expect(chip(results(page), 'Channel 102')).toHaveAttribute('aria-pressed', 'false');
	// By name and by tag too.
	await search.fill('Channel 3005');
	await expect(results(page).getByRole('listitem').first()).toHaveText(/^ *Channel 3005/);
	await search.fill('');
	await expect(onList(page)).toBeVisible();
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
