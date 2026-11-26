/// <reference types="node" />
// The Recent page's filters on a phone (375 x 812), as the owner found them
// on Android (2026-11-27; ADR 0049; ArchiveUx 0.6.0), against the binary
// with the test fixture (recent.spec.ts has the rest of the page):
//
// - with Start listening showing, the calendar's From time and To time can
//   be reached, and nothing on any page stays behind the bar and the bottom
//   navigation once scrolled to its foot;
// - the error summary names each field with a link that moves the focus to
//   it, and a talkgroups problem shows by the option it is about;
// - a listening set of more than 1,000 talkgroups filters (it was refused
//   at 1,000), and one with every talkgroup on asks with no filter at all.
import { test, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const SAVED = 'radioarchive.listening-set';
const PHONE = { width: 375, height: 812 };

const rows = (page: Page) => page.getByRole('list', { name: 'Recent calls' }).getByRole('listitem');

async function withSet(page: Page, set: { id: number }[]) {
	await page.addInitScript(([k, s]) => localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s })), [SAVED, set] as const);
}

const openFilters = async (page: Page) => {
	const toggle = page.getByRole('button', { name: 'Filters', exact: true });
	if ((await toggle.getAttribute('aria-expanded')) !== 'true') await toggle.click();
};

/** The page in a theme, then axe at WCAG 2.1 AA. */
async function axeIn(page: Page, what: string) {
	for (const theme of ['light', 'dark']) {
		await page.evaluate((t) => (document.documentElement.dataset.theme = t), theme);
		const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
		expect(results.violations, `${what}, ${theme}`).toEqual([]);
	}
}

/** Whether the middle of an element is the element itself, not something drawn over it. */
const onTop = (page: Page, selector: string) =>
	page.evaluate((sel) => {
		const el = document.querySelector<HTMLElement>(sel)!;
		const r = el.getBoundingClientRect();
		const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
		return !!hit && (hit === el || el.contains(hit));
	}, selector);

/** The top of what's fixed at the foot of a phone's screen: the dock (Start listening) above the bottom bar. */
const dockTop = (page: Page) => page.evaluate(() => document.querySelector('section.dock')!.getBoundingClientRect().top);

test.describe('phones', () => {
	test.use({ viewport: PHONE });

	test("the calendar's times can be reached with Start listening showing", async ({ page }) => {
		await withSet(page, [{ id: 1001 }]);
		await page.goto('/recent');
		await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
		await openFilters(page);
		await page.getByRole('button', { name: 'Dates' }).click();
		const calendar = page.getByRole('dialog', { name: 'Choose dates' });
		await expect(calendar).toBeVisible();
		// Today, twice: one day.
		const today = calendar.locator('button.day.today');
		await today.click();
		await today.click();
		const from = calendar.getByLabel('From time');
		const to = calendar.getByLabel('To time');
		await expect(from).toBeEnabled();
		// The calendar ends above the dock, and nothing is drawn over either time.
		const box = (await calendar.boundingBox())!;
		expect(box.y + box.height).toBeLessThanOrEqual(await dockTop(page));
		expect(box.y).toBeGreaterThanOrEqual(0);
		for (const t of [from, to]) {
			await t.scrollIntoViewIfNeeded();
			const r = (await t.boundingBox())!;
			expect(r.y + r.height, 'a time above the dock').toBeLessThanOrEqual(await dockTop(page));
			expect(r.y, 'a time on the screen').toBeGreaterThanOrEqual(0);
		}
		expect(await onTop(page, `#${await from.getAttribute('id')}`)).toBe(true);
		expect(await onTop(page, `#${await to.getAttribute('id')}`)).toBe(true);
		await from.fill('09:00');
		await to.fill('17:30');
		await axeIn(page, 'the calendar open on a phone');
		await calendar.getByRole('button', { name: 'Done' }).click();
		await expect(page.getByRole('button', { name: /^Dates: .* 09:00 – 17:30$/ })).toBeVisible();
	});

	for (const path of ['/', '/recent', '/recent?filters', '/basket', '/conversation']) {
		test(`${path}: scrolled to its foot, nothing stays behind the dock and the bottom bar`, async ({ page }) => {
			await withSet(page, [{ id: 1001 }, { id: 1002 }]);
			await page.goto(path === '/recent?filters' ? '/recent' : path);
			await expect(page.getByRole('button', { name: 'Start listening' })).toBeVisible();
			if (path === '/recent?filters') await openFilters(page);
			if (path.startsWith('/recent')) await expect(rows(page).first()).toBeVisible();
			await page.waitForLoadState('networkidle');
			await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
			const foot = await page.evaluate(() => {
				let bottom = 0;
				for (const el of document.querySelectorAll<HTMLElement>('main *')) {
					const s = getComputedStyle(el);
					if (s.position === 'fixed' || s.visibility === 'hidden' || !el.offsetParent) continue;
					const r = el.getBoundingClientRect();
					if (r.height > 0) bottom = Math.max(bottom, r.bottom);
				}
				return bottom;
			});
			expect(foot, 'the page\'s last content, scrolled to the foot').toBeLessThanOrEqual(await dockTop(page));
		});
	}

	test('the error summary names the field, links to it, and the problem shows by the option it is about', async ({ page }) => {
		await withSet(page, [{ id: 1001 }]);
		await page.goto('/recent');
		await openFilters(page);
		// The listener's own problem: These talkgroups, with none chosen.
		await page.getByRole('radio', { name: 'These talkgroups' }).check();
		await page.getByRole('button', { name: 'Show calls' }).click();
		const summary = page.locator('.error-summary');
		await expect(summary).toBeFocused();
		await expect(summary.getByRole('heading', { name: '1 field needs attention' })).toBeVisible();
		const link = summary.getByRole('link', { name: 'Talkgroups: Choose at least one talkgroup, or another option above.' });
		await expect(link).toHaveAttribute('href', '#recent-talkgroup');
		await axeIn(page, 'the error summary');
		await page.keyboard.press('Tab');
		await expect(link).toBeFocused();
		await page.keyboard.press('Enter');
		await expect(page.getByRole('combobox', { name: 'Add a talkgroup' })).toBeFocused();
		await expect(page.getByRole('combobox', { name: 'Add a talkgroup' })).toHaveAttribute('aria-invalid', 'true');
		await expect(page.getByRole('combobox', { name: 'Add a talkgroup' })).toBeInViewport();

		// The server's, about the listening set: under Your listening set, not under These talkgroups.
		await page.route('**/api/v1/recent-calls**', (r) =>
			r.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'too_many_talkgroups', message: 'Name at most 10000 talkgroups at once.' }) })
		);
		await page.goto('/recent');
		// A field's refusal opens the filters by itself, with the problem shown.
		const error = page.locator('#tg-error');
		await expect(error).toBeVisible();
		await expect(error).toHaveText('Name at most 10000 talkgroups at once.');
		await expect(page.getByRole('button', { name: 'Filters', exact: true })).toHaveAttribute('aria-expanded', 'true');
		// Straight after the option it's about, before the next option.
		const order = await page.evaluate(() => {
			const all = [...document.querySelectorAll('#scope-set, #tg-error, #scope-all')];
			return all.map((e) => e.id);
		});
		expect(order).toEqual(['scope-set', 'tg-error', 'scope-all']);
		const setLink = summary.getByRole('link', { name: 'Talkgroups: Name at most 10000 talkgroups at once.' });
		await expect(setLink).toHaveAttribute('href', '#scope-set');
		await setLink.click();
		await expect(page.getByRole('radio', { name: /^Your listening set/ })).toBeFocused();
		await expect(page.getByRole('radio', { name: /^Your listening set/ })).toBeInViewport();

		// The dates: the link leads to the calendar's button.
		await page.unroute('**/api/v1/recent-calls**');
		await page.goto('/recent?from=2026-10-01&to=2026-11-01');
		await expect(page.locator('#dates-error')).toBeVisible();
		await summary.getByRole('link', { name: /^Date and time: Search at most 31 days at once/ }).click();
		await expect(page.locator('#recent-dates')).toBeFocused();
		await expect(page.locator('#recent-dates')).toHaveAttribute('aria-describedby', /dates-error/);
	});
});

/**
 * The fixture's catalogue with n made-up public talkgroups added in the
 * browser (2001 on), so the listening set can be larger than the fixture.
 * The server's catalogue is its own: it reads the made-up ones as unknown.
 */
async function biggerCatalogue(page: Page, n: number) {
	let catalogue: { talkgroups: { id: number }[] } | undefined;
	await page.route('**/api/v1/catalogue', async (route) => {
		const r = await route.fetch();
		const c = await r.json();
		const base = c.talkgroups[0];
		for (let i = 0; i < n; i++) c.talkgroups.push({ ...base, id: 2001 + i, label: `TG ${2001 + i}`, name: `Made-up ${2001 + i}`, groups: [], fireground_channel: null });
		catalogue = c;
		await route.fulfill({ response: r, json: c });
	});
	return () => catalogue!;
}

/** Every search the page sends, as the API's parameters. */
function searches(page: Page) {
	const sent: { method: string; talkgroups?: string[] }[] = [];
	page.on('request', (r) => {
		const u = new URL(r.url());
		if (u.pathname !== '/api/v1/recent-calls') return;
		if (r.method() === 'GET') sent.push({ method: 'GET', talkgroups: u.searchParams.get('talkgroups')?.split(',') });
		else sent.push({ method: r.method(), talkgroups: JSON.parse(r.postData() ?? '{}').talkgroups });
	});
	return sent;
}

test.describe('a large listening set', () => {
	test.use({ viewport: PHONE });

	test('1,212 talkgroups filter, as a POST body (refused at 1,000 before ADR 0049)', async ({ page }) => {
		await biggerCatalogue(page, 1300);
		// 1001 and 1,211 made-up ones: 1,212, more than ADR 0013's 1,000, not every one.
		await withSet(page, [{ id: 1001 }, ...Array.from({ length: 1211 }, (_, i) => ({ id: 2001 + i }))]);
		const sent = searches(page);
		await page.goto('/recent');
		await expect(rows(page)).toHaveCount(50);
		await expect(page.locator('.summary')).toContainText('Your listening set (1212 talkgroups)');
		await expect(page.locator('.error-summary, #tg-error')).toHaveCount(0);
		// Only 1001's calls: the set filtered.
		const names = await rows(page).evaluateAll((els) => els.map((e) => e.textContent ?? ''));
		expect(names.every((t) => t.includes('Dispatch North'))).toBe(true);
		expect(sent[0]).toMatchObject({ method: 'POST' });
		expect(sent[0].talkgroups).toHaveLength(1212);
	});

	test('All on: every talkgroup in the set asks with no filter', async ({ page }) => {
		const fixture = await page.request.get('/api/v1/catalogue').then((r) => r.json());
		const every = [...(fixture.talkgroups as { id: number }[]).map((t) => ({ id: t.id })), ...Array.from({ length: 1300 }, (_, i) => ({ id: 2001 + i }))];
		await biggerCatalogue(page, 1300);
		await withSet(page, every);
		const sent = searches(page);
		await page.goto('/recent');
		await expect(rows(page)).toHaveCount(50);
		await expect(page.locator('.summary')).toContainText(`Your listening set (${every.length} talkgroups)`);
		await expect(page.locator('.error-summary, #tg-error')).toHaveCount(0);
		expect(sent[0]).toMatchObject({ method: 'GET', talkgroups: undefined });
		// The same calls as every talkgroup: 1002's among them.
		await page.goto('/recent?talkgroups=all');
		await expect(rows(page)).toHaveCount(50);
	});
});
