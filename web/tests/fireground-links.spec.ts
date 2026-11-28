/// <reference types="node" />
// A fireground link naming several channels (SPEC.md R101, R33, R47; A15,
// A49; ADR 0057): /?fireground=508&fireground=540&fireground=542, as
// PagerArchive's one Listen button opens it. Against the binary's fixture
// first (1002 "Fireground 41" with 41, hidden 1003 with 43, ignored 1004
// with 44), then on a made-up catalogue served in place of the API's, with
// a dispatch channel and two fireground channels, the way a real page
// names them.
import { test, expect, type BrowserContext, type Page, type Request } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';

const SAVED = 'radioarchive.listening-set';
const SESSION = 'radioarchive.session-additions';

const sessionList = (page: Page) => page.getByRole('list', { name: 'Added for this session' });
const notice = (page: Page) => page.getByRole('status').filter({ hasText: /added for this session|Added for this session|in your listening set/ });
const sessionIds = (page: Page) =>
	page.evaluate((k) => (JSON.parse(sessionStorage.getItem(k) ?? 'null')?.talkgroups ?? []).map((e: { id: number }) => e.id), SESSION);

/** The requests the page makes for fireground numbers, as their n= values. */
function lookups(page: Page) {
	const seen: string[][] = [];
	page.on('request', (r: Request) => {
		const u = new URL(r.url());
		if (u.pathname.startsWith('/api/v1/fireground')) seen.push(u.pathname === '/api/v1/fireground' ? u.searchParams.getAll('n') : [u.pathname]);
	});
	return seen;
}

/** Nothing on the page names hidden 1003 or ignored 1004. */
async function namesNothingHidden(page: Page) {
	const text = await page.locator('body').innerText();
	for (const s of ['1003', 'Admin', 'ADMIN', '1004', 'TG 1004', '43', '44']) expect(text, s).not.toContain(s);
}

test.describe('the fixture', () => {
	test('A49: available channels are added at once and the rest counted, never named; the link leaves the address', async ({ page }) => {
		const asked = lookups(page);
		await page.goto('/?fireground=43&fireground=41&fireground=44&fireground=99&fireground=x41');
		await expect(notice(page)).toHaveText(/^Added for this session: Fireground 41\. Start listening to hear it\.\s*4 channels aren't available\.$/);
		await expect(sessionList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
		await expect(page).toHaveURL((u) => !u.searchParams.has('fireground'));
		await namesNothingHidden(page);
		// One request for them all, the malformed one not sent.
		expect(asked).toEqual([['43', '41', '44', '99']]);
		// A reload reads nothing again, and the session's addition stays.
		await page.reload();
		await expect(sessionList(page).getByRole('listitem')).toHaveText([/Fireground 41/]);
		await expect(notice(page)).toHaveCount(0);
	});

	test('A49: a number twice, leading zeros not counting, is one channel, as a single link is', async ({ page }) => {
		const asked = lookups(page);
		await page.goto('/?fireground=41&fireground=041&fireground=0041');
		await expect(page.getByRole('status').filter({ hasText: 'Fireground 41 is added for this session.' })).toBeVisible();
		expect(asked).toEqual([['41']]);
		await expect(page).toHaveURL((u) => !u.searchParams.has('fireground'));
	});

	test('A49: only the first 10 values are read', async ({ page }) => {
		const ten = ['90', '91', '92', '93', '94', '95', '96', '97', '98', '99'];
		const asked = lookups(page);
		await page.goto(`/?${[...ten, '41'].map((n) => `fireground=${n}`).join('&')}`);
		await expect(page.getByText("This channel isn't available.")).toBeVisible();
		expect(asked).toEqual([ten]);
		expect(await sessionIds(page)).toEqual([]);
	});

	test("A15, A49: when none is available, one \"This channel isn't available\", the same page as one unknown number's", async ({ page }) => {
		await page.goto('/?fireground=99');
		await expect(page.getByText("This channel isn't available.")).toBeVisible();
		await expect(page.getByLabel('Search talkgroups')).toBeVisible();
		const one = await page.locator('main').innerHTML();
		await page.goto('/?fireground=43&fireground=44&fireground=x&fireground=99');
		await expect(page.getByText("This channel isn't available.")).toBeVisible();
		await expect(page.getByLabel('Search talkgroups')).toBeVisible();
		expect(await page.locator('main').innerHTML()).toBe(one);
		await namesNothingHidden(page);
		expect(await sessionIds(page)).toEqual([]);
	});

	test('R101: a single number still reads as it did, and leaves the address', async ({ page }) => {
		await page.goto('/?fireground=041');
		await expect(page.getByRole('status').filter({ hasText: 'Fireground 41 is added for this session. Start listening to hear it.' })).toBeVisible();
		await expect(page).toHaveURL((u) => !u.searchParams.has('fireground'));
	});
});

// The made-up deployment: VF08's dispatch channel and two fireground
// channels with numbers, and one talkgroup with none.
function talkgroup(id: number, name: string, label: string, tag: string, fireground: number | null) {
	return { id, label, name, tag, groups: ['Fire district 8'], led_colour: null, alert: null, delay_minutes: 0, fireground_channel: fireground, quiet: false };
}
const catalogue = {
	time_zone: 'Australia/Melbourne',
	quiet_window_days: 7,
	groups: ['Fire district 8'],
	tags: ['dispatch', 'fireground'],
	talkgroups: [
		talkgroup(3508, 'VF08 – Dispatch', 'VF08 DISP', 'dispatch', 508),
		talkgroup(3540, 'CH 540', 'FG 540', 'fireground', 540),
		talkgroup(3542, 'CH 542', 'FG 542', 'fireground', 542),
		talkgroup(3001, 'Ops 1', 'OPS 1', 'dispatch', null)
	]
};

/** Serves the made-up catalogue and its fireground lookup, as the server does. */
async function madeUp(page: Page, set: { id: number }[] = []) {
	await page.route('**/api/v1/catalogue', (r) => r.fulfill({ json: catalogue }));
	await page.route(
		(u) => u.pathname === '/api/v1/fireground',
		(r) => {
			const seen = new Set<number>();
			const talkgroups = [];
			for (const s of new URL(r.request().url()).searchParams.getAll('n').slice(0, 10)) {
				const n = Number(s);
				const t = catalogue.talkgroups.find((t) => t.fireground_channel === n);
				if (t && !seen.has(n)) talkgroups.push(t);
				seen.add(n);
			}
			return r.fulfill({ json: { talkgroups }, headers: { 'Cache-Control': 'no-store' } });
		}
	);
	await page.addInitScript(
		([k, s]) => {
			if (sessionStorage.getItem('seeded')) return;
			sessionStorage.setItem('seeded', '1');
			if ((s as unknown[]).length) localStorage.setItem(k as string, JSON.stringify({ v: 1, talkgroups: s }));
		},
		[SAVED, set] as const
	);
}

const link = (...ns: string[]) => `/?${ns.map((n) => `fireground=${n}`).join('&')}`;

test.describe('a page naming several channels', () => {
	test('A49: the dispatch channel and both fireground channels, in one notice, with the one Start listening', async ({ page }) => {
		await madeUp(page);
		const asked = lookups(page);
		await page.goto(link('508', '540', '542'));
		await expect(notice(page)).toHaveText('Added for this session: VF08 – Dispatch, CH 540, CH 542. Start listening to hear them.');
		await expect(sessionList(page).getByRole('listitem')).toHaveText([/VF08 – Dispatch/, /CH 540/, /CH 542/]);
		expect(await sessionIds(page)).toEqual([3508, 3540, 3542]);
		expect(asked).toEqual([['508', '540', '542']]);
		// One button starts it all: the dock's, as on every page.
		await expect(page.getByRole('button', { name: 'Start listening' })).toHaveCount(1);
		await expect(page.getByRole('region', { name: 'Player' }).getByRole('button', { name: 'Start listening' })).toBeVisible();
		await expect(page).toHaveURL((u) => !u.searchParams.has('fireground'));
	});

	test('A49: one in the set already, and some not available', async ({ page }) => {
		await madeUp(page, [{ id: 3540 }]);
		await page.goto(link('508', '540', '999', 'abc'));
		await expect(notice(page)).toHaveText(
			/^Added for this session: VF08 – Dispatch\. Already in your listening set: CH 540\. Start listening to hear them\.\s*2 channels aren't available\.$/
		);
		expect(await sessionIds(page)).toEqual([3508]);
	});

	test('A49: duplicates are one channel each', async ({ page }) => {
		await madeUp(page);
		const asked = lookups(page);
		await page.goto(link('508', '0508', '540', '540'));
		await expect(notice(page)).toHaveText('Added for this session: VF08 – Dispatch, CH 540. Start listening to hear them.');
		expect(asked).toEqual([['508', '540']]);
	});

	test('A49: an eleventh channel is ignored', async ({ page }) => {
		await madeUp(page);
		const asked = lookups(page);
		await page.goto(link('901', '902', '903', '904', '905', '906', '907', '908', '909', '508', '540'));
		await expect(notice(page)).toHaveText(/^Added for this session: VF08 – Dispatch\. Start listening to hear it\.\s*9 channels aren't available\.$/);
		expect(asked[0]).toHaveLength(10);
		expect(await sessionIds(page)).toEqual([3508]);
	});

	test("A49: none available is \"This channel isn't available\"", async ({ page }) => {
		await madeUp(page);
		await page.goto(link('901', '902'));
		await expect(page.getByText("This channel isn't available.")).toBeVisible();
		await expect(sessionList(page)).toHaveCount(0);
	});
});

async function axe(page: Page, width: number) {
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
	const results = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa']).analyze();
	expect(results.violations).toEqual([]);
}

/** The pull request's screenshots (RA_SHOTS names a folder). */
async function shot(page: Page, name: string) {
	if (!process.env.RA_SHOTS || test.info().project.name !== 'chromium') return;
	const size = page.viewportSize()!;
	await page.mouse.move(0, 0);
	await page.evaluate(() => window.scrollTo(0, 0));
	const height = await page.evaluate(() => document.documentElement.scrollHeight);
	await page.setViewportSize({ width: size.width, height: Math.max(height, size.height) });
	await page.screenshot({ path: `${process.env.RA_SHOTS}/${name}.png` });
	await page.setViewportSize(size);
}

async function theme(context: BrowserContext, baseURL: string, value: string) {
	await context.addCookies([{ name: 'theme', value, url: new URL(baseURL).origin }]);
}

for (const t of ['light', 'dark'] as const)
	for (const width of [375, 1280] as const)
		test(`axe: a link naming several channels, ${t}, ${width}px`, async ({ page, context, baseURL }) => {
			await theme(context, baseURL!, t);
			await page.setViewportSize({ width, height: 900 });
			await madeUp(page, [{ id: 3540 }]);
			await page.goto(link('508', '540', '542', '999'));
			await expect(page.locator('html')).toHaveAttribute('data-theme', t);
			await expect(notice(page)).toContainText("1 channel isn't available.");
			await axe(page, width);
			await shot(page, `several-${t}-${width}`);
		});
